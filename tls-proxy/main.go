// HTTPS front end for the projector remote.
//
// Three jobs:
//  1. Terminates TLS on :8443 -> busybox httpd on 127.0.0.1:8790 (the CGIs).
//  2. Gates every request on a shared token, because the CGIs inject key events
//     as root and anything on the WiFi can reach this port.
//  3. Injects pointer events itself, natively. The `m` CGI cost ~5 fork+execs
//     per move batch (sh -> cat -> three sendevents) on a 1.0 GHz ARMv7; that
//     was the air-mouse jitter. Here the evdev fd stays open and a move is one
//     48-byte write.
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
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
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

const (
	listen   = ":8443"
	backend  = "http://127.0.0.1:8790"
	dir      = "/data/local/tmp/tvremote"
	defHost  = "192.168.220.53:8443"
	ip       = "192.168.220.53"
	mdnsHost = "projectorremote" // advertised as projectorremote.local

	caFile    = dir + "/ca.crt"
	tokenFile = dir + "/token"
	cookie    = "tvr"
)

// ---------------------------------------------------------------- auth

// token is read once at start. Empty means the device has no token file; we
// then refuse everything except /ca.crt with a 503 that says why, rather than
// failing open -- serving root key injection to the whole LAN is the bug this
// gate exists to close.
var token string

func loadToken() {
	b, err := os.ReadFile(tokenFile)
	if err != nil {
		log.Printf("token: %v -- REFUSING ALL REQUESTS, run ./deploy.sh", err)
		return
	}
	token = strings.TrimSpace(string(b))
	if token == "" {
		log.Printf("token: %s is empty -- REFUSING ALL REQUESTS", tokenFile)
	}
}

func tokenOK(v string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(v), []byte(token)) == 1
}

// serveCA hands out the local CA. Never gated: it is a public certificate, and
// gating it would make a fresh phone unable to bootstrap trust.
func serveCA(w http.ResponseWriter) {
	b, err := os.ReadFile(caFile)
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

// findNode resolves an input device by NAME rather than by a fixed event
// number, which changes across reboots.
func findNode(name string) (string, error) {
	b, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return "", err
	}
	return parseDevices(string(b), name)
}

// parseDevices pulls one device's event node out of /proc/bus/input/devices.
func parseDevices(s, name string) (string, error) {
	match := false
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(ln, "N: "):
			match = strings.Contains(ln, `Name="`+name+`"`)
		case match && strings.HasPrefix(ln, "H: "):
			for _, f := range strings.Fields(ln) {
				f = strings.TrimPrefix(f, "Handlers=")
				if strings.HasPrefix(f, "event") {
					return "/dev/input/" + f, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no %q node in /proc/bus/input/devices", name)
}

// evdev owns one long-lived write fd on an input node.
type evdev struct {
	name string
	mu   sync.Mutex
	f    *os.File
}

func (d *evdev) fd() (*os.File, error) {
	if d.f != nil {
		return d.f, nil
	}
	p, err := findNode(d.name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	log.Printf("evdev: %s (%s) open", p, d.name)
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

var (
	mo = &evdev{name: "Hi mouse"}    // pointer
	kb = &evdev{name: "Hi keyboard"} // remote keys (the IR receiver's node)
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

// keys serves /cgi-bin/k. Only the held variant is native; every plain keycode
// still goes to the CGI unchanged.
func keys(cgi http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "long_ok" {
			cgi.ServeHTTP(w, r)
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

// pointer serves /cgi-bin/m for the three air-mouse ops. `tap`/`swipe` still
// need the `input` binary, so those fall through to the CGI unchanged.
func pointer(cgi http.Handler) http.Handler {
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
			cgi.ServeHTTP(w, r) // tap / swipe / anything else
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
	out, err := exec.Command(cmdBin, "package", "query-activities", "--brief",
		"-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER").Output()
	if err != nil {
		return nil, err
	}
	m := parseActivities(string(out))
	if len(m) == 0 {
		return nil, errors.New("no launcher activities found")
	}
	a.comp, a.fetched = m, time.Now()
	return m, nil
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
func apps(cgi http.Handler) http.Handler {
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

		default:
			cgi.ServeHTTP(w, r)
		}
	})
}

// ---------------------------------------------------------------- routing

// routes allowlists what the backend may serve.
//
// busybox httpd is started with -h <install dir>, so it will happily serve
// everything in there: key.pem (the TLS private key), token (this gate's own
// secret), boot.sh, the logs, the proxy binary. File modes don't help -- httpd
// runs as root. So only the paths the UI actually uses are proxied; anything
// else is a 404 even with a valid cookie.
func routes(cgi http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	// Patterns without a trailing slash are exact matches in ServeMux.
	mux.Handle("/cgi-bin/m", pointer(cgi)) // rel/click/wheel native, tap/swipe fall through
	mux.Handle("/cgi-bin/k", keys(cgi))    // held OK is native, plain keys fall through
	mux.Handle("/cgi-bin/t", cgi)          // text
	mux.Handle("/cgi-bin/a", apps(cgi))    // app list + launch-by-package, shortcuts fall through
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html":
			cgi.ServeHTTP(w, r)
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
	host, path := defHost, "/"
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
			if ca, e := os.ReadFile(caFile); e == nil {
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

func main() {
	loadToken()

	b, err := url.Parse(backend)
	if err != nil {
		log.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(dir+"/cert.pem", dir+"/key.pem")
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatal(err)
	}
	// advertise projectorremote.local (A -> ip) over mDNS/Bonjour so iOS can use a
	// hostname instead of the bare IP (Safari won't show IP certs as secure).
	if s, err := zeroconf.RegisterProxy("Projector Remote", "_https._tcp", "local.",
		8443, mdnsHost, []string{ip}, []string{"path=/"}, nil); err != nil {
		log.Printf("mdns: %v", err)
	} else {
		defer s.Shutdown()
		log.Printf("mDNS: %s.local -> %s", mdnsHost, ip)
	}

	srv := &http.Server{Handler: gate(routes(httputil.NewSingleHostReverseProxy(b)))}
	tlsLn := tls.NewListener(splitListener{ln}, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	log.Printf("HTTPS %s -> %s (token gate on, native pointer, plaintext redirects)", listen, backend)
	log.Fatal(srv.Serve(tlsLn))
}
