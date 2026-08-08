package main

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The kernel's struct input_event is 16 bytes on this 32-bit ARM target
// (timeval = 2x32-bit long). Getting this wrong makes the kernel misparse the
// batch, so pin the exact bytes.
func TestEvLayout(t *testing.T) {
	got := hex.EncodeToString(ev(evRel, relY, -3))
	// 8 zero bytes (time) | type 0200 | code 0100 | value fdffffff (-3 LE)
	want := "0000000000000000" + "0200" + "0100" + "fdffffff"
	if got != want {
		t.Fatalf("ev(EV_REL,REL_Y,-3) = %s, want %s", got, want)
	}
	if len(ev(evSyn, synReport, 0)) != 16 {
		t.Fatalf("event size = %d, want 16", len(ev(evSyn, synReport, 0)))
	}
}

// Real /proc/bus/input/devices layout from the projector: the pointer and the
// remote's key receiver are different nodes, and both are resolved by name
// because the event numbers move across reboots.
const inputDevices = `I: Bus=0000 Vendor=0001 Product=0001 Version=0100
N: Name="Hi keyboard"
H: Handlers=kbd event0
I: Bus=0000 Vendor=0001 Product=0002 Version=0100
N: Name="Hi mouse"
P: Phys=
H: Handlers=mouse0 event1
B: EV=7
I: Bus=0006 Vendor=046d Product=0002 Version=0000
N: Name="Hi Keypad"
H: Handlers=kbd event4
`

func TestParseDevices(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"Hi mouse", "/dev/input/event1"},
		{"Hi keyboard", "/dev/input/event0"},
		{"Hi Keypad", "/dev/input/event4"},
	} {
		got, err := parseDevices(inputDevices, c.name)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s -> %q, want %q", c.name, got, c.want)
		}
	}

	// Handlers can list the event node first, in which case the field carries
	// the "Handlers=" prefix.
	got, err := parseDevices("N: Name=\"Hi mouse\"\nH: Handlers=event7 mouse0\n", "Hi mouse")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/dev/input/event7" {
		t.Fatalf("got %q, want /dev/input/event7", got)
	}

	if _, err := parseDevices(inputDevices, "No Such Device"); err == nil {
		t.Fatal("expected an error when the named node does not exist")
	}
	// a name must not match a different device's line by prefix
	if p, err := parseDevices(inputDevices, "Hi key"); err == nil {
		t.Errorf(`"Hi key" matched %q, want no match (names are exact)`, p)
	}
}

// The held-OK press is a key DOWN then a key UP on the keyboard node; pin the
// encoding of both, since a missing UP would wedge the projector's UI.
func TestHeldKeyEncoding(t *testing.T) {
	down := hex.EncodeToString(ev(evKey, keyEnter, 1))
	up := hex.EncodeToString(ev(evKey, keyEnter, 0))
	// type 0100 (EV_KEY) | code 1c00 (28 = KEY_ENTER -> DPAD_CENTER) | value
	if want := "0000000000000000" + "0100" + "1c00" + "01000000"; down != want {
		t.Errorf("key down = %s, want %s", down, want)
	}
	if want := "0000000000000000" + "0100" + "1c00" + "00000000"; up != want {
		t.Errorf("key up = %s, want %s", up, want)
	}
	if keyEnter != 28 {
		t.Errorf("keyEnter = %d, want 28 (KEY_ENTER, mapped to DPAD_CENTER)", keyEnter)
	}
}

func gated() http.Handler {
	return gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // marker: request reached the backend
	}))
}

func TestGateRejectsUntokenedRequests(t *testing.T) {
	token = "s3cret"
	defer func() { token = "" }()

	for _, path := range []string{"/", "/cgi-bin/k?power", "/cgi-bin/m?rel=1,1", "/index.html"} {
		w := httptest.NewRecorder()
		gated().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401", path, w.Code)
		}
	}

	// wrong token in the query is a 403, not a cookie grant
	w := httptest.NewRecorder()
	gated().ServeHTTP(w, httptest.NewRequest("GET", "/?t=wrong", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("bad token: got %d, want 403", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Error("bad token must not set a cookie")
	}
}

func TestGateExchangesTokenForCookie(t *testing.T) {
	token = "s3cret"
	defer func() { token = "" }()

	w := httptest.NewRecorder()
	gated().ServeHTTP(w, httptest.NewRequest("GET", "/?t=s3cret", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want / (token must not stay in the URL)", loc)
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Value != token {
		t.Fatalf("cookies = %+v, want one holding the token", cs)
	}
	c := cs[0]
	if !c.Secure || !c.HttpOnly {
		t.Errorf("cookie must be Secure+HttpOnly, got Secure=%v HttpOnly=%v", c.Secure, c.HttpOnly)
	}

	// and that cookie then gets through
	w = httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/cgi-bin/k?power", nil)
	r.AddCookie(c)
	gated().ServeHTTP(w, r)
	if w.Code != http.StatusTeapot {
		t.Fatalf("cookie request got %d, want it to reach the backend", w.Code)
	}
}

// The backend serves the whole install dir. A valid cookie must NOT be enough to
// pull the TLS key, the token itself, or the logs back out of it.
func TestRoutesDoNotExposeTheInstallDir(t *testing.T) {
	token = "s3cret"
	defer func() { token = "" }()

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // marker: reached the busybox backend
	})
	h := gate(routes(backend))
	c := &http.Cookie{Name: cookie, Value: token}

	get := func(path string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(c)
		h.ServeHTTP(w, r)
		return w.Code
	}

	for _, path := range []string{
		"/key.pem", "/token", "/cert.pem", "/boot.sh", "/boot.log",
		"/httpd.log", "/bin/tlsproxy", "/cgi-bin/",
	} {
		if got := get(path); got != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404 (must not be proxied to the backend)", path, got)
		}
	}

	// ServeMux cleans a traversal and 301s to the cleaned path, which then hits
	// the 404 branch. What matters is that it never reaches the backend.
	if got := get("/cgi-bin/k/../../token"); got == http.StatusTeapot {
		t.Error("path traversal reached the backend")
	} else if got != http.StatusMovedPermanently {
		t.Errorf("traversal: got %d, want 301 to the cleaned path or 404", got)
	}

	for _, path := range []string{"/", "/index.html", "/cgi-bin/k?power", "/cgi-bin/t?hi", "/cgi-bin/a?vlc"} {
		if got := get(path); got != http.StatusTeapot {
			t.Errorf("%s: got %d, want it proxied to the backend", path, got)
		}
	}
}

// long_ok must be handled natively. If it ever fell through to the CGI it would
// get a 400 there and the hold gesture would be a silently dead button.
func TestLongOkIsHandledNatively(t *testing.T) {
	reached := ""
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.RawQuery
	})
	h := keys(backend)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/cgi-bin/k?long_ok", nil))
	if reached != "" {
		t.Errorf("long_ok fell through to the CGI as %q", reached)
	}
	// No evdev node on the test host, so it must fail loudly rather than lie.
	if w.Code == http.StatusOK {
		t.Error("long_ok reported success with no input node available")
	}

	// every plain key still goes to the CGI untouched
	for _, q := range []string{"ok", "power", "volup", "rewind"} {
		reached = ""
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/cgi-bin/k?"+q, nil))
		if reached != q {
			t.Errorf("%q did not reach the CGI (got %q)", q, reached)
		}
	}
}

// Real `cmd package query-activities --brief` output from the projector.
const queryActivities = `18 activities found:
  com.newlink.cast/.activity.MainActivity
  com.zhiying.bluetoothmodelservice/.WelcomeActivity
  org.smarttube.stable/com.liskovsoft.smartyoutubetv2.tv.ui.main.SplashActivity
  com.brouken.player/.PlayerActivity
  com.example.cinepulse/.MainActivity
  com.lonelycatgames.Xplore/.Browser
  com.vectorunit.purple.googleplay/.MainActivity
  org.videolan.vlc/.StartActivity
`

func TestParseActivities(t *testing.T) {
	m := parseActivities(queryActivities)
	if len(m) != 8 {
		t.Fatalf("parsed %d entries, want 8: %v", len(m), m)
	}
	for pkg, want := range map[string]string{
		"org.videolan.vlc":      "org.videolan.vlc/.StartActivity",
		"com.example.cinepulse": "com.example.cinepulse/.MainActivity",
		// fully-qualified activity in a different package namespace
		"org.smarttube.stable":      "org.smarttube.stable/com.liskovsoft.smartyoutubetv2.tv.ui.main.SplashActivity",
		"com.lonelycatgames.Xplore": "com.lonelycatgames.Xplore/.Browser",
	} {
		if m[pkg] != want {
			t.Errorf("%s -> %q, want %q", pkg, m[pkg], want)
		}
	}
	// the header line must not become an entry
	for pkg := range m {
		if strings.Contains(pkg, " ") {
			t.Errorf("parsed junk key %q", pkg)
		}
	}
}

// Anything that isn't a plain package/activity pair must be dropped, so a
// malformed or hostile line can never end up in the allowlist.
func TestParseActivitiesRejectsJunk(t *testing.T) {
	junk := `18 activities found:
  priority=0 preferredOrder=0 match=0x108000 specificIndex=-1 isDefault=true
  ../../etc/passwd/x
  com.evil/;reboot
  com.evil/../../x
  com.evil pkg/.A
  /.NoPackage
  com.ok/
`
	if m := parseActivities(junk); len(m) != 0 {
		t.Fatalf("expected nothing parsed, got %v", m)
	}
}

// launch() must reject a malformed package name before it execs anything --
// this test runs on a host with no /system/bin/cmd, so reaching exec would fail
// differently and the assertion below would not hold.
func TestLaunchRejectsMalformedPackageWithoutExec(t *testing.T) {
	a := &appList{}
	for _, bad := range []string{
		"", "com.evil;reboot", "com.evil/../x", "com.evil x", "-com.evil",
		"com.evil$(id)", "com.evil`id`", "com.evil|sh", "../../etc/passwd",
	} {
		if err := a.launch(bad); !errors.Is(err, errBadPkg) {
			t.Errorf("launch(%q) = %v, want errBadPkg", bad, err)
		}
	}
	// A well-formed name gets past validation and on to the lookup (which fails
	// here for want of the device) -- proving validation is the only gate skipped.
	if err := a.launch("org.videolan.vlc"); errors.Is(err, errBadPkg) {
		t.Error("a well-formed package was rejected as malformed")
	}
}

// No token file on the device must fail closed, not open.
func TestGateFailsClosedWithoutToken(t *testing.T) {
	token = ""
	w := httptest.NewRecorder()
	gated().ServeHTTP(w, httptest.NewRequest("GET", "/cgi-bin/k?power", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", w.Code)
	}
	// an empty token must never satisfy the compare
	if tokenOK("") {
		t.Fatal("empty token accepted")
	}
}
