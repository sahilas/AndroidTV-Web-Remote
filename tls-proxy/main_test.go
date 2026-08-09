package main

import (
	"bytes"
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

// Nothing is served off the filesystem any more, so the install dir holding
// key.pem and the token is not a document root at all. This test is what stops
// somebody reintroducing a static file handler over it.
func TestRoutesServeNoFilesystemPath(t *testing.T) {
	token = "s3cret"
	defer func() { token = "" }()

	h := gate(routes())
	c := &http.Cookie{Name: cookie, Value: token}

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(c)
		h.ServeHTTP(w, r)
		return w
	}

	for _, path := range []string{
		"/key.pem", "/token", "/cert.pem", "/boot.sh", "/boot.log",
		"/httpd.log", "/bin/tlsproxy", "/cgi-bin/", "/config",
	} {
		if got := get(path).Code; got != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, got)
		}
	}

	// ServeMux cleans a traversal and 301s to the cleaned path, which then hits
	// the 404 branch. What matters is that it never yields content.
	if got := get("/cgi-bin/k/../../token").Code; got != http.StatusMovedPermanently && got != http.StatusNotFound {
		t.Errorf("traversal: got %d, want 301 to the cleaned path or 404", got)
	}

	// The UI is the one thing that IS served, and from memory rather than disk.
	for _, path := range []string{"/", "/index.html"} {
		w := get(path)
		if w.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", path, w.Code)
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("<html")) {
			t.Errorf("%s did not serve the embedded page", path)
		}
	}
}

// The embedded page must actually be embedded. An empty or truncated blob still
// compiles and still 200s -- it just serves a blank remote.
func TestEmbeddedUIIsPresent(t *testing.T) {
	if len(indexHTML) < 1000 {
		t.Fatalf("embedded index.html is %d bytes, expected the real page", len(indexHTML))
	}
	for _, want := range []string{"cgi-bin/k?", "cgi-bin/m?rel=", "cgi-bin/a?list", "/caps"} {
		if !bytes.Contains(indexHTML, []byte(want)) {
			t.Errorf("embedded page does not reference %q -- UI and server are out of sync", want)
		}
	}
}

// long_ok goes to evdev; everything else to `input`. An unknown name must 400
// rather than being passed to the injector.
func TestKeyRouting(t *testing.T) {
	h := keys()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/cgi-bin/k?long_ok", nil))
	// No evdev node on the test host, so it must fail loudly rather than lie.
	if w.Code == http.StatusOK {
		t.Error("long_ok reported success with no input node available")
	}

	// Includes shapes that would matter if this were ever concatenated into a
	// shell command rather than passed as an argv element.
	for _, q := range []string{"bogus", "", "keyevent%2026", "26", "ok;reboot", "ok%26reboot"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/cgi-bin/k?"+q, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%q: got %d, want 400 (unknown key must not reach the injector)", q, w.Code)
		}
	}

	// Names the UI sends must all resolve. A missing entry here is a dead button.
	for _, q := range []string{"up", "down", "left", "right", "ok", "back", "home",
		"menu", "power", "volup", "voldown", "mute", "play", "prev", "next",
		"rewind", "ff", "del", "enter", "space", "search"} {
		if _, found := keyCodes[q]; !found {
			t.Errorf("keyCodes is missing %q, which the UI sends", q)
		}
	}
}

// decodeQuery has to stay forgiving: the shell CGI passed malformed escapes
// through, and turning a stray '%' into a 400 would break typing mid-word.
func TestDecodeQuery(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"hello", "hello"},
		{"a+b", "a b"},
		{"a%20b", "a b"},
		{"%41%42", "AB"},
		{"100%", "100%"},      // trailing bare percent, not an error
		{"%zz", "%zz"},        // invalid hex passes through
		{"a%2", "a%2"},        // truncated escape passes through
		{"caf%C3%A9", "café"}, // utf-8 survives byte-wise decoding
	} {
		if got := decodeQuery(c.in); got != c.want {
			t.Errorf("decodeQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// tap/swipe take numbers straight off the network, so the validation is the
// only thing between a request and an `input` argv.
func TestTapSwipeValidation(t *testing.T) {
	bad := []struct{ op, val string }{
		{"tap", "1"},             // too few
		{"tap", "1,2,3"},         // too many
		{"tap", "a,2"},           // non-numeric
		{"tap", "-1,2"},          // negative
		{"tap", "99999,2"},       // absurd
		{"swipe", "1,2,3"},       // too few
		{"swipe", "1,2,3,4,5,6"}, // too many
		{"bogus", "1,2"},         // unknown op
	}
	for _, c := range bad {
		w := httptest.NewRecorder()
		tapSwipe(w, c.op, c.val)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s=%s: got %d, want 400", c.op, c.val, w.Code)
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

// mDNS must advertise the port we actually bound. A wrong value here fails
// silently: the service resolves and then nothing connects to it.
func TestListenPort(t *testing.T) {
	for _, c := range []struct {
		addr string
		want int
	}{
		{":8443", 8443},
		{"0.0.0.0:8443", 8443},
		{"127.0.0.1:9000", 9000},
		{"[::]:8443", 8443},
		{"8443", 0},      // no colon -- not a valid listen address
		{":", 0},         // empty port
		{":notaport", 0}, // non-numeric
		{":0", 0},        // port 0 means "any", nothing to advertise
		{":70000", 0},    // out of range
	} {
		if got := listenPort(c.addr); got != c.want {
			t.Errorf("listenPort(%q) = %d, want %d", c.addr, got, c.want)
		}
	}
}

// defHost only backstops a plaintext request with no Host header. It must still
// carry the configured port, or the redirect points at the wrong service.
func TestDefHost(t *testing.T) {
	oIP, oLn := advIP, listen
	defer func() { advIP, listen = oIP, oLn }()

	advIP, listen = "10.0.0.5", ":9443"
	if got, want := defHost(), "10.0.0.5:9443"; got != want {
		t.Errorf("defHost() = %q, want %q", got, want)
	}
	advIP = ""
	if got, want := defHost(), "localhost:9443"; got != want {
		t.Errorf("defHost() with no IP = %q, want %q", got, want)
	}
}
