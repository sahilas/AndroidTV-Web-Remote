// Everything that used to be a busybox CGI shell script, done natively.
//
// The CGIs are gone because busybox is not a thing you can rely on: it lives at
// /vendor/bin/busybox on this projector, is absent entirely on many boxes, and
// even when present is often built without the httpd applet. Serving from the
// Go binary removes that dependency -- and with it the whole hazard class where
// `httpd -h <install dir>` cheerfully served key.pem and the token, which is
// what the route allowlist existed to paper over.
package main

import (
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Android's input command. Absolute path: PATH is not dependable for a process
// started by init.
const inputBin = "/system/bin/input"

// Injecting via `input` forks app_process (a JVM start) per call -- tens of ms.
// That is fine for discrete presses, and is why the air-mouse does NOT come
// through here: it writes evdev directly. See pointer() in main.go.
const inputTimeout = 10 * time.Second

// keyCodes maps the UI's button names to Android keycodes. This is the list the
// CGI carried; the names are the wire format the UI already speaks.
var keyCodes = map[string]int{
	"up": 19, "down": 20, "left": 21, "right": 22, "ok": 23,
	"back": 4, "home": 3, "menu": 82, "power": 26,
	"volup": 24, "voldown": 25, "mute": 164,
	"play": 85, "prev": 88, "next": 87, "rewind": 89, "ff": 90,
	"del": 67, "enter": 66, "space": 62, "search": 84,
}

// runInput execs `input <args>`. Nothing from the network reaches a shell:
// exec.Command takes an argv slice, so there is no word splitting to exploit,
// and every caller validates its arguments before getting here anyway.
func runInput(args ...string) error {
	cmd := exec.Command(inputBin, args...)
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(inputTimeout):
		_ = cmd.Process.Kill()
		return errInputTimeout
	}
}

var errInputTimeout = errTimeout{}

type errTimeout struct{}

func (errTimeout) Error() string { return "input command timed out" }

// ok writes the 3-byte "ok\n" body the UI expects.
func ok(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Length", "3")
	w.Write([]byte("ok\n"))
}

// plainKey handles /cgi-bin/k?<name> for every key except the held variant.
func plainKey(w http.ResponseWriter, name string) {
	code, found := keyCodes[name]
	if !found {
		http.Error(w, "unknown key", http.StatusBadRequest)
		return
	}
	if err := runInput("keyevent", strconv.Itoa(code)); err != nil {
		log.Printf("keyevent %s(%d): %v", name, code, err)
		http.Error(w, "key injection failed", http.StatusInternalServerError)
		return
	}
	ok(w)
}

// decodeQuery undoes application/x-www-form-urlencoded by hand.
//
// net/url.QueryUnescape is not used: it rejects a lone '%' and any malformed
// escape, which would turn a stray percent typed by the user into a 400 for the
// whole string. The shell CGI silently passed those through, and matching that
// keeps typing forgiving.
func decodeQuery(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '+':
			b.WriteByte(' ')
		case s[i] == '%' && i+2 < len(s):
			n, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				b.WriteByte(s[i])
				continue
			}
			b.WriteByte(byte(n))
			i += 2
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// maxText bounds a single typing request. Each character is its own fork, so an
// unbounded string is a free way to wedge the box for minutes.
const maxText = 256

// typeText handles /cgi-bin/t?<urlencoded>.
//
// Lowercased on purpose: this device latches caps ON for the rest of the string
// after any capital sent through `input text`, corrupting everything following
// it. Never pressing shift avoids the bug entirely, and TV search boxes and URL
// hosts are case-insensitive. Documented limit -- you cannot type a
// case-sensitive password with this.
//
// Space goes via keyevent 62 rather than `input text " "`, where it can be
// eaten as an argument separator.
func typeText(w http.ResponseWriter, raw string) {
	txt := strings.ToLower(decodeQuery(raw))
	if len(txt) > maxText {
		txt = txt[:maxText]
	}
	for _, c := range txt {
		var err error
		if c == ' ' {
			err = runInput("keyevent", "62")
		} else {
			err = runInput("text", string(c))
		}
		if err != nil {
			log.Printf("text %q: %v", string(c), err)
			http.Error(w, "text injection failed", http.StatusInternalServerError)
			return
		}
	}
	ok(w)
}

// absMax bounds tap/swipe coordinates. Screens are not this big; anything
// larger is a malformed or hostile request.
const absMax = 10000

// tapSwipe handles the legacy absolute pointer ops. The UI does not use these --
// it drives the air-mouse instead -- but they are kept working because they were
// part of the interface before.
func tapSwipe(w http.ResponseWriter, op, val string) {
	nums := strings.Split(val, ",")
	args := make([]string, 0, 5)
	switch op {
	case "tap":
		if len(nums) != 2 {
			http.Error(w, "tap needs x,y", http.StatusBadRequest)
			return
		}
		args = append(args, "tap")
	case "swipe":
		// x1,y1,x2,y2 with an optional duration in ms.
		if len(nums) != 4 && len(nums) != 5 {
			http.Error(w, "swipe needs x1,y1,x2,y2[,ms]", http.StatusBadRequest)
			return
		}
		args = append(args, "swipe")
	default:
		http.Error(w, "unknown pointer op", http.StatusBadRequest)
		return
	}
	for _, n := range nums {
		v, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil || v < 0 || v > absMax {
			http.Error(w, "bad coordinate", http.StatusBadRequest)
			return
		}
		args = append(args, strconv.Itoa(v))
	}
	if err := runInput(args...); err != nil {
		log.Printf("%s %s: %v", op, val, err)
		http.Error(w, "pointer command failed", http.StatusInternalServerError)
		return
	}
	ok(w)
}

// openSettings handles /cgi-bin/a?set.
//
// Separate from the app list because on this build no package exposes a
// LAUNCHER activity for Settings at all, so it cannot be launched by package
// the way every other app is. The action intent resolves to whatever the box's
// own settings app happens to be, which is the portable way to ask.
func openSettings(w http.ResponseWriter) {
	cmd := exec.Command(cmdBin, "activity", "start-activity", "-a", "android.settings.SETTINGS")
	out, err := cmd.CombinedOutput()
	// `am`/`cmd activity` prints "Error:" but does not reliably exit nonzero, so
	// the output has to be checked too.
	if err != nil || strings.Contains(string(out), "Error:") {
		log.Printf("settings: %v: %s", err, strings.TrimSpace(string(out)))
		http.Error(w, "could not open settings", http.StatusInternalServerError)
		return
	}
	ok(w)
}
