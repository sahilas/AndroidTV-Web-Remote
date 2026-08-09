// Self-contained HTTPS server for the Android TV web remote.
//
// Three jobs:
//  1. Terminates TLS and serves the entire UI from this one binary. The page is
//     embedded, so there is no document root and no busybox dependency -- which
//     is both what makes it portable and what makes the old "httpd -h <dir>
//     serves key.pem" hazard structurally impossible.
//  2. Gates every request on a shared token, because this injects input as root
//     and anything on the WiFi can reach the port.
//  3. Injects pointer and held-key events straight to evdev. The old `m` CGI
//     cost ~5 fork+execs per move batch (sh -> cat -> three sendevents) on a
//     1.0 GHz ARMv7; that was the air-mouse jitter. Here the fd stays open and
//     a move is one 48-byte write. Discrete keys still go via Android's
//     `input`, where one fork per press is not noticeable.
//
// Plaintext HTTP that lands on :8443 gets a 301 to https:// -- except /ca.crt,
// which is served in the clear because the phone cannot trust our TLS until it
// has that file.
//
// Static binary, no deps beyond zeroconf. Build with ../build.sh.
package main

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	_ "embed" // for the //go:embed directive on indexHTML
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

// Runtime configuration. These are vars, not consts, because the same binary has
// to run on any box -- boot.sh passes the values from the device's config file.
// The defaults are what a bare `./tlsproxy` with no flags gets, so the binary is
// still runnable by hand for debugging.
var (
	listen   = ":8443"
	dir      = "/data/local/tmp/tvremote"
	advIP    = "" // advertised over mDNS; empty = autodetect this host's LAN IP
	mdnsHost = "androidtvremote"
)

const cookie = "tvr"

// Derived from dir, so they cannot be consts. Functions rather than vars because
// dir is not final until flag.Parse has run.
func caFile() string    { return dir + "/ca.crt" }
func tokenFile() string { return dir + "/token" }

// defHost is only used to build the redirect target for a plaintext request that
// arrived with no Host header -- essentially only hand-rolled clients. A real
// browser always sends one and that value wins.
func defHost() string {
	if advIP == "" {
		return "localhost" + listen
	}
	return advIP + listen
}

// ---------------------------------------------------------------- auth

// token is read once at start. Empty means the device has no token file; we
// then refuse everything except /ca.crt with a 503 that says why, rather than
// failing open -- serving root key injection to the whole LAN is the bug this
// gate exists to close.
var token string

func loadToken() {
	b, err := os.ReadFile(tokenFile())
	if err != nil {
		log.Printf("token: %v -- REFUSING ALL REQUESTS, run ./deploy.sh", err)
		return
	}
	token = strings.TrimSpace(string(b))
	if token == "" {
		log.Printf("token: %s is empty -- REFUSING ALL REQUESTS", tokenFile())
	}
}

func tokenOK(v string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(v), []byte(token)) == 1
}

// serveCA hands out the local CA. Never gated: it is a public certificate, and
// gating it would make a fresh phone unable to bootstrap trust.
func serveCA(w http.ResponseWriter) {
	b, err := os.ReadFile(caFile())
	if err != nil {
		http.Error(w, "no ca.crt on device", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

// gate wraps the whole mux. `?t=<token>` exchanges the token for a cookie and
// 302s to / so the secret leaves the URL bar, history and any Referer.
func gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ca.crt" {
			serveCA(w)
			return
		}
		if token == "" {
			http.Error(w, "no token file on device -- run ./deploy.sh", http.StatusServiceUnavailable)
			return
		}
		if t := r.URL.Query().Get("t"); t != "" {
			if !tokenOK(t) {
				http.Error(w, "bad token", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name: cookie, Value: token, Path: "/",
				Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
				MaxAge: 10 * 365 * 24 * 3600,
			})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		c, err := r.Cookie(cookie)
		if err != nil || !tokenOK(c.Value) {
			// Plain 401, no WWW-Authenticate: we do not want a browser
			// credential prompt, we want the tokenized URL to be reopened.
			http.Error(w, "unauthorized -- open the tokenized URL again", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- evdev pointer

// Linux input event codes we use.
const (
	evSyn = 0
	evKey = 1
	evRel = 2

	synReport = 0
	relX      = 0
	relY      = 1
	relWheel  = 8
	btnLeft   = 0x110 // BTN_MOUSE / BTN_LEFT
)

// struct input_event is 16 bytes on 32-bit ARM:
//
//	struct timeval { long tv_sec; long tv_usec; }  = 8
//	__u16 type; __u16 code;                        = 4
//	__s32 value;                                   = 4
//
// The time field is left zero on purpose -- the input core stamps injected
// events itself. Encoding by hand (not a Go struct) keeps the layout exact.
const evSize = 16

func ev(typ, code uint16, value int32) []byte {
	b := make([]byte, evSize)
	binary.LittleEndian.PutUint16(b[8:], typ)
	binary.LittleEndian.PutUint16(b[10:], code)
	binary.LittleEndian.PutUint32(b[12:], uint32(value))
	return b
}

// evdev owns one long-lived write fd on an input node.
//
// role is what this device is for ("pointer"/"keys"); prefer is an optional
// exact device name that overrides capability detection. The node is resolved
// lazily and re-resolved after a write error, so a device that renumbers or
// re-enumerates recovers without a restart.
type evdev struct {
	role   string
	prefer string
	want   func(inputDev) bool
	mu     sync.Mutex
	f      *os.File
}

func (d *evdev) fd() (*os.File, error) {
	if d.f != nil {
		return d.f, nil
	}
	b, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return nil, err
	}
	devs := parseInputDevices(string(b))
	dev, err := pickNode(devs, d.prefer, d.want)
	if err != nil {
		return nil, fmt.Errorf("%s: %w (available:%s)", d.role, err, describe(devs))
	}
	f, err := os.OpenFile(dev.node, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	log.Printf("evdev: %s -> %s (%q)", d.role, dev.node, dev.name)
	d.f = f
	return f, nil
}

// emit writes one batch. On a write error the fd is dropped and re-resolved
// once: the node disappears on a USB re-enumerate or device renumber, and the
// stale fd then fails with EBADF/ENODEV forever if we keep it.
func (d *evdev) emit(evs ...[]byte) error {
	buf := make([]byte, 0, len(evs)*evSize)
	for _, e := range evs {
		buf = append(buf, e...)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var last error
	for try := 0; try < 2; try++ {
		f, err := d.fd()
		if err != nil {
			return err
		}
		if _, err = f.Write(buf); err == nil {
			return nil
		}
		last = err
		f.Close()
		d.f = nil
	}
	return last
}

// Discovered by capability, not by name -- see discover.go. -pointer-name and
// -key-name pin them by exact name if a box needs it.
var (
	mo = &evdev{role: "pointer", want: inputDev.isPointer}
	kb = &evdev{role: "keys", want: inputDev.isKeyboard}
)

// A genuinely HELD key, which is what makes an app open its context menu.
//
// `input keyevent --longpress` does NOT do this: it injects down, a repeat with
// FLAG_LONG_PRESS, and up all at the SAME timestamp, so an app that measures
// down->up elapsed time sees 0 ms and treats it as a tap. Measured on this box:
// in X-plore it just toggled the folder, and in SmartTube it started playing the
// video -- both plain selects. Holding the key on the evdev node instead gives
// real elapsed time and kernel autorepeat, exactly like the physical remote, and
// X-plore then opens its context menu.
//
// KEY_ENTER (28) is DPAD_CENTER per /vendor/usr/keylayout/Vendor_0001_Product_0001.kl,
// which is the layout for "Hi keyboard" (Bus=0000 Vendor=0001 Product=0001).
// 800 ms is margin, not a fix: Android's default long-press timeout is 500 ms,
// and durations from 600 ms to 2 s were measured to behave identically, so
// whether a menu appears depends on the app, not on holding longer.
const (
	keyEnter = 28
	holdDur  = 800 * time.Millisecond
)

func holdKey(d *evdev, code uint16, dur time.Duration) error {
	if err := d.emit(ev(evKey, code, 1), ev(evSyn, synReport, 0)); err != nil {
		return err
	}
	// The lock is NOT held across the sleep -- emit takes it per call.
	time.Sleep(dur)
	up := func() error { return d.emit(ev(evKey, code, 0), ev(evSyn, synReport, 0)) }
	if err := up(); err != nil {
		// A key left logically down would wedge the whole UI, so try once more.
		log.Printf("hold %d: release failed (%v), retrying", code, err)
		return up()
	}
	return nil
}

// keys serves /cgi-bin/k. The held variant goes to evdev; every other key is an
// `input keyevent`.
func keys() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "long_ok" {
			plainKey(w, r.URL.RawQuery)
			return
		}
		if err := holdKey(kb, keyEnter, holdDur); err != nil {
			log.Printf("long_ok: %v", err)
			http.Error(w, "hold failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "3")
		io.WriteString(w, "ok\n")
	})
}

const relMax = 2000 // a single batch should never exceed a screen; reject abuse

// pointer serves /cgi-bin/m. The three air-mouse ops are written straight to
// evdev; `tap`/`swipe` need the `input` binary and are handled in input.go.
func pointer() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op, val, _ := strings.Cut(r.URL.RawQuery, "=")
		var err error
		switch op {
		case "rel":
			xs, ys, ok := strings.Cut(val, ",")
			if !ok {
				http.Error(w, "no", http.StatusBadRequest)
				return
			}
			x, e1 := strconv.Atoi(xs)
			y, e2 := strconv.Atoi(ys)
			if e1 != nil || e2 != nil || abs(x) > relMax || abs(y) > relMax {
				http.Error(w, "no", http.StatusBadRequest)
				return
			}
			err = mo.emit(ev(evRel, relX, int32(x)), ev(evRel, relY, int32(y)), ev(evSyn, synReport, 0))
		case "wheel":
			n, e := strconv.Atoi(val)
			if e != nil || abs(n) > 10 {
				http.Error(w, "no", http.StatusBadRequest)
				return
			}
			err = mo.emit(ev(evRel, relWheel, int32(n)), ev(evSyn, synReport, 0))
		case "click":
			err = mo.emit(
				ev(evKey, btnLeft, 1), ev(evSyn, synReport, 0),
				ev(evKey, btnLeft, 0), ev(evSyn, synReport, 0))
		default:
			tapSwipe(w, op, val) // tap / swipe / anything else
			return
		}
		if err != nil {
			log.Printf("pointer %s: %v", op, err)
			http.Error(w, "pointer unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "3")
		io.WriteString(w, "ok\n")
	})
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ---------------------------------------------------------------- app launching

// The favourites row lets the phone pick any installed app, so unlike the fixed
// `case` labels in cgi-bin/a this endpoint does take a package name from the
// request. Three things keep that safe:
//
//   - charset validation (a package name, nothing else)
//   - the package must appear in the DEVICE'S OWN list of launcher activities,
//     so the allowlist is still authoritative -- it just comes from the system
//     instead of being hardcoded. Only apps the TV's launcher would itself show
//     can be started, and the activity is the resolver's, never the caller's.
//   - no shell: /system/bin/cmd is exec'd with an argv array. (/system/bin/am is
//     only a shell wrapper around `cmd activity "$@"`, so calling cmd directly
//     skips a process and any shell parsing.)
const cmdBin = "/system/bin/cmd"

var (
	pkgRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)*$`)
	actRe = regexp.MustCompile(`^\.?[A-Za-z0-9_$]+(\.[A-Za-z0-9_$]+)*$`)

	errBadPkg    = errors.New("malformed package name")
	errNotLaunch = errors.New("package has no launcher activity on this device")
)

// parseActivities reads `cmd package query-activities --brief` output into
// package -> "package/activity". Lines that aren't a plain component are
// ignored, including the "N activities found:" header.
func parseActivities(s string) map[string]string {
	m := map[string]string{}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		pkg, act, ok := strings.Cut(ln, "/")
		if !ok || !pkgRe.MatchString(pkg) || !actRe.MatchString(act) {
			continue
		}
		if _, dup := m[pkg]; !dup {
			m[pkg] = ln // first match wins, mirroring launcher order
		}
	}
	return m
}

// appList caches the launcher-activity map. It only changes when something is
// installed or removed, so a short TTL plus a forced refresh on lookup miss is
// enough -- and it keeps a launch from paying the ~130 ms query every press.
type appList struct {
	mu      sync.Mutex
	comp    map[string]string
	fetched time.Time
}

const appTTL = 30 * time.Second

func (a *appList) get(force bool) (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !force && a.comp != nil && time.Since(a.fetched) < appTTL {
		return a.comp, nil
	}
	m := queryLaunchers()
	if len(m) == 0 {
		return nil, errors.New("no launcher activities found")
	}
	a.comp, a.fetched = m, time.Now()
	return m, nil
}

// Android TV apps declare LEANBACK_LAUNCHER; phone-style apps declare LAUNCHER.
// Neither is a superset of the other, so querying one alone hides apps.
//
// Measured: on a Google Android TV image com.android.tv.settings appears ONLY
// under LEANBACK_LAUNCHER, so querying LAUNCHER alone made Settings unlaunchable.
// On the HiSilicon projector the reverse holds -- three packages
// (com.newlink.cast, com.newlink.filemanager, com.zhiying.bluetoothmodelservice)
// appear only under LAUNCHER. Fire OS is TV-first and is expected to look like
// the former, which is why this is a general fix rather than a Fire TV special
// case.
var launcherCategories = []string{
	"android.intent.category.LEANBACK_LAUNCHER", // TV-native; wins on a tie
	"android.intent.category.LAUNCHER",
}

// queryLaunchers merges both categories. LEANBACK is queried first so that on a
// box where an app declares both, the TV-flavoured activity is the one launched.
//
// A failing category is skipped rather than fatal: a non-TV build may not know
// LEANBACK_LAUNCHER at all, and losing the whole app list over that would be a
// worse outcome than a shorter one.
func queryLaunchers() map[string]string {
	outs := make([]string, 0, len(launcherCategories))
	for _, cat := range launcherCategories {
		out, err := exec.Command(cmdBin, "package", "query-activities", "--brief",
			"-a", "android.intent.action.MAIN", "-c", cat).Output()
		if err != nil {
			log.Printf("app list: %s: %v", cat, err)
			continue
		}
		outs = append(outs, string(out))
	}
	return mergeActivities(outs...)
}

// mergeActivities unions parsed query output, earlier arguments winning ties.
// Split out from queryLaunchers so the merge is testable without a device.
func mergeActivities(outs ...string) map[string]string {
	merged := map[string]string{}
	for _, out := range outs {
		for pkg, comp := range parseActivities(out) {
			if _, dup := merged[pkg]; !dup {
				merged[pkg] = comp
			}
		}
	}
	return merged
}

func (a *appList) launch(pkg string) error {
	if !pkgRe.MatchString(pkg) {
		return errBadPkg
	}
	m, err := a.get(false)
	if err != nil {
		return err
	}
	comp, ok := m[pkg]
	if !ok {
		// might have been installed since the last query -- one forced refresh
		if m, err = a.get(true); err != nil {
			return err
		}
		if comp, ok = m[pkg]; !ok {
			return errNotLaunch
		}
	}
	// `cmd activity` prints "Error: ..." while still exiting 0, so check output.
	out, err := exec.Command(cmdBin, "activity", "start-activity", "-n", comp).CombinedOutput()
	if err != nil {
		return fmt.Errorf("start %s: %v: %s", comp, err, out)
	}
	if bytes.Contains(out, []byte("Error")) || bytes.Contains(out, []byte("Exception")) {
		return fmt.Errorf("start %s: %s", comp, out)
	}
	return nil
}

var al = &appList{}

// apps serves /cgi-bin/a. `?list` and `?pkg=` are native; the fixed shortcut
// labels (?cine, ?vlc, ?just, ?next, ?set) still fall through to the CGI, which
// is also where Settings lives -- it has no launcher activity on this build and
// so cannot come from the list.
func apps() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.RawQuery == "list":
			m, err := al.get(false)
			if err != nil {
				log.Printf("app list: %v", err)
				http.Error(w, "cannot list apps", http.StatusInternalServerError)
				return
			}
			pkgs := make([]string, 0, len(m))
			for p := range m {
				pkgs = append(pkgs, p)
			}
			sort.Strings(pkgs)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(pkgs)

		case r.URL.Query().Get("pkg") != "":
			err := al.launch(r.URL.Query().Get("pkg"))
			switch {
			case err == nil:
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Content-Length", "3")
				io.WriteString(w, "ok\n")
			case errors.Is(err, errBadPkg):
				http.Error(w, "bad package name", http.StatusBadRequest)
			case errors.Is(err, errNotLaunch):
				http.Error(w, "not an installed launchable app", http.StatusNotFound)
			default:
				log.Printf("app launch: %v", err)
				http.Error(w, "launch failed", http.StatusInternalServerError)
			}

		case r.URL.RawQuery == "set":
			openSettings(w)

		default:
			http.Error(w, "unknown app request", http.StatusBadRequest)
		}
	})
}

// ---------------------------------------------------------------- routing

// indexHTML is compiled into the binary. Nothing is served off the filesystem,
// so the install directory holding key.pem and token is not a document root at
// all -- the previous design's central hazard cannot recur by accident.
//
//go:embed ui/index.html
var indexHTML []byte

// routes is the complete surface. Every path is handled in-process; there is no
// backend to proxy to and no static file root.
func routes() *http.ServeMux {
	mux := http.NewServeMux()
	// Patterns without a trailing slash are exact matches in ServeMux.
	mux.Handle("/cgi-bin/m", pointer()) // rel/click/wheel evdev, tap/swipe via `input`
	mux.Handle("/cgi-bin/k", keys())    // held OK evdev, plain keys via `input`
	mux.Handle("/cgi-bin/t", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		typeText(w, r.URL.RawQuery)
	}))
	mux.Handle("/cgi-bin/a", apps()) // list, launch-by-package, settings
	// What this box can actually do. The UI reads it to hide controls that
	// cannot work here, instead of offering a Touchpad tab that 500s on every
	// drag -- which is what a locked box would otherwise present.
	mux.HandleFunc("/caps", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(probeCaps())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(indexHTML)))
			w.Write(indexHTML)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

// ---------------------------------------------------------------- TLS / plaintext split

// firstByteConn replays the one byte we peeked to classify the connection.
type firstByteConn struct {
	net.Conn
	first    byte
	consumed bool
}

func (c *firstByteConn) Read(p []byte) (int, error) {
	if !c.consumed {
		c.consumed = true
		if len(p) == 0 {
			return 0, nil
		}
		p[0] = c.first
		if len(p) > 1 {
			n, err := c.Conn.Read(p[1:])
			return n + 1, err
		}
		return 1, nil
	}
	return c.Conn.Read(p)
}

// splitListener returns only TLS connections to the http server; plaintext
// requests are handled inline (301, or ca.crt in the clear).
type splitListener struct{ net.Listener }

func (l splitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		b := make([]byte, 1)
		if _, err := io.ReadFull(c, b); err != nil {
			c.Close()
			continue
		}
		fc := &firstByteConn{Conn: c, first: b[0]}
		if b[0] == 0x16 { // TLS ClientHello record type
			return fc, nil
		}
		go plaintext(fc)
	}
}

func plaintext(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	host, path := defHost(), "/"
	req, err := http.ReadRequest(bufio.NewReader(c))
	if err == nil {
		if req.Host != "" {
			host = req.Host
		}
		path = req.URL.RequestURI()

		// The CA has to be fetchable without TLS: the phone can't validate our
		// certificate until this file is installed and trusted. This is the
		// bootstrap path that lets busybox httpd stay bound to localhost.
		if req.URL.Path == "/ca.crt" {
			if ca, e := os.ReadFile(caFile()); e == nil {
				fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: application/x-x509-ca-cert\r\n"+
					"Content-Length: %d\r\nConnection: close\r\n\r\n", len(ca))
				c.Write(ca)
				return
			}
		}
	}
	io.WriteString(c, "HTTP/1.1 301 Moved Permanently\r\nLocation: https://"+host+path+
		"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
}

// ---------------------------------------------------------------- main

// listenPort pulls the port out of a listen address so mDNS advertises the port
// we are actually bound to. Announcing a hardcoded 8443 while listening on
// something else sends every mDNS client to a closed port and fails silently --
// the service resolves, then nothing connects.
func listenPort(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}

// lanIP finds this host's primary non-loopback IPv4. Used when -ip is not given,
// so a box that got its address from DHCP still advertises the right one over
// mDNS without anybody having to configure it.
func lanIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() {
			continue
		}
		if v4 := n.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

func main() {
	flag.StringVar(&listen, "listen", listen, "HTTPS listen address, e.g. :8443")
	flag.StringVar(&dir, "dir", dir, "on-device install directory")
	flag.StringVar(&advIP, "ip", advIP, "IP advertised over mDNS (empty = autodetect)")
	flag.StringVar(&mdnsHost, "mdns-host", mdnsHost, "mDNS name, advertised as <name>.local")
	flag.StringVar(&mo.prefer, "pointer-name", "", "exact input device name for the pointer (default: autodetect by capability)")
	flag.StringVar(&kb.prefer, "key-name", "", "exact input device name for key injection (default: autodetect by capability)")
	flag.Parse()

	if advIP == "" {
		advIP = lanIP()
	}

	loadToken()

	cert, err := tls.LoadX509KeyPair(dir+"/cert.pem", dir+"/key.pem")
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatal(err)
	}
	// advertise <mdnsHost>.local (A -> advIP) over mDNS/Bonjour so iOS can use a
	// hostname instead of the bare IP (Safari won't show IP certs as secure).
	//
	// The advertised port is parsed back out of `listen` rather than hardcoded:
	// announcing 8443 while actually listening elsewhere sends every mDNS client
	// to a closed port, and it fails silently.
	port := listenPort(listen)
	if advIP == "" {
		log.Printf("mdns: no non-loopback IPv4 found -- not advertising")
	} else if port == 0 {
		log.Printf("mdns: cannot parse a port out of %q -- not advertising", listen)
	} else if s, err := zeroconf.RegisterProxy("Projector Remote", "_https._tcp", "local.",
		port, mdnsHost, []string{advIP}, []string{"path=/"}, nil); err != nil {
		log.Printf("mdns: %v", err)
	} else {
		defer s.Shutdown()
		log.Printf("mDNS: %s.local -> %s:%d", mdnsHost, advIP, port)
	}

	srv := &http.Server{Handler: gate(routes())}
	tlsLn := tls.NewListener(splitListener{ln}, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	c := probeCaps()
	log.Printf("caps: keys=%v pointer=%v heldKey=%v %s", c.Keys, c.Pointer, c.HeldKey, c.Detail)
	log.Printf("HTTPS %s (self-contained: token gate, embedded UI, native input)", listen)
	log.Fatal(srv.Serve(tlsLn))
}
