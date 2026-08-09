// Finding the right /dev/input node on a box we have never seen.
//
// The old code asked for Name="Hi mouse" and Name="Hi keyboard", which are
// HiSilicon's names for this projector's virtual devices. On any other box
// those do not exist, and the air-mouse and held-OK are silently dead buttons.
//
// So classify by capability instead: a pointer is whatever declares EV_REL with
// REL_X and REL_Y; a key device is whatever declares EV_KEY with KEY_ENTER.
// That is a property of what the device can do, not of what a vendor called it.
package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Bit positions in the EV capability mask.
const (
	bitEvKey = 1 // EV_KEY
	bitEvRel = 2 // EV_REL
)

// inputDev is one stanza of /proc/bus/input/devices.
type inputDev struct {
	name string
	node string // /dev/input/eventN
	ev   uint64 // low word of B: EV=
	key  uint64 // low word of B: KEY=
	rel  uint64 // low word of B: REL=
}

// lowWord returns the least-significant word of a kernel-printed bitmap.
//
// The kernel prints these high word first, space separated, each as %lx with no
// zero padding, and skips leading all-zero words. The word WIDTH is
// BITS_PER_LONG, so it is 32 on a 32-bit box and 64 on a 64-bit one -- which
// means a bit above 31 cannot be located without knowing the kernel's word size,
// and the format gives no way to tell.
//
// We sidestep that entirely by only ever testing bits that live in the low word:
// EV_KEY(1), EV_REL(2), REL_X(0), REL_Y(1), KEY_ENTER(28). All are < 32, so the
// last printed word holds them under either width. BTN_LEFT(0x110) would NOT be
// safe to test here, which is why classification does not use it.
func lowWord(s string) uint64 {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseUint(f[len(f)-1], 16, 64)
	if err != nil {
		return 0
	}
	return v
}

func hasBit(mask uint64, bit uint) bool { return mask&(1<<bit) != 0 }

// parseInputDevices reads every stanza of /proc/bus/input/devices.
func parseInputDevices(s string) []inputDev {
	var out []inputDev
	var cur *inputDev
	flush := func() {
		if cur != nil && cur.node != "" {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(ln, "I: "):
			flush()
			cur = &inputDev{}
		case cur == nil:
			continue
		case strings.HasPrefix(ln, "N: Name="):
			cur.name = strings.Trim(strings.TrimPrefix(ln, "N: Name="), `"`)
		case strings.HasPrefix(ln, "H: "):
			for _, f := range strings.Fields(strings.TrimPrefix(ln, "H: ")) {
				f = strings.TrimPrefix(f, "Handlers=")
				if strings.HasPrefix(f, "event") {
					cur.node = "/dev/input/" + f
				}
			}
		case strings.HasPrefix(ln, "B: EV="):
			cur.ev = lowWord(strings.TrimPrefix(ln, "B: EV="))
		case strings.HasPrefix(ln, "B: KEY="):
			cur.key = lowWord(strings.TrimPrefix(ln, "B: KEY="))
		case strings.HasPrefix(ln, "B: REL="):
			cur.rel = lowWord(strings.TrimPrefix(ln, "B: REL="))
		}
	}
	flush()
	return out
}

// isPointer: declares relative motion on both axes. Deliberately does not test
// BTN_LEFT -- see lowWord for why that bit is not reachable portably. Emitting a
// click to a device that has no button is harmless; picking the wrong node
// because we could not read the bit would not be.
func (d inputDev) isPointer() bool {
	return hasBit(d.ev, bitEvRel) && hasBit(d.rel, relX) && hasBit(d.rel, relY)
}

// isKeyboard: declares KEY_ENTER, which is what the held-OK gesture sends.
//
// Excludes anything that also does relative motion. A combo device would
// otherwise be a candidate for both roles, and holding a key on the pointer node
// does nothing visible -- a dead button that looks like a code bug.
func (d inputDev) isKeyboard() bool {
	return hasBit(d.ev, bitEvKey) && hasBit(d.key, keyEnter) && !hasBit(d.ev, bitEvRel)
}

// pickNode chooses the node for a role.
//
// preferName wins when it matches, so a box where autodetection guesses wrong
// can be pinned via -pointer-name/-key-name without a code change. Otherwise the
// first capable device wins, in kernel enumeration order -- the same order the
// kernel itself would hand to a userspace reader.
func pickNode(devs []inputDev, preferName string, want func(inputDev) bool) (inputDev, error) {
	if preferName != "" {
		for _, d := range devs {
			if d.name == preferName {
				if !want(d) {
					return inputDev{}, fmt.Errorf("device %q exists but lacks the required capabilities", preferName)
				}
				return d, nil
			}
		}
		return inputDev{}, fmt.Errorf("no input device named %q", preferName)
	}
	for _, d := range devs {
		if want(d) {
			return d, nil
		}
	}
	return inputDev{}, fmt.Errorf("no suitable input device among %d candidates", len(devs))
}

// caps is what this box can actually do, resolved by trying rather than by
// assuming. Both evdev features depend on writing /dev/input, and whether that
// is allowed is not knowable from uid or group membership alone:
//
// On a Permissive box the `input` group is enough and everything works as the
// shell user. On an Enforcing box -- which is what a retail Android TV is --
// SELinux policy denies the shell domain both /dev/input and /dev/uinput, so
// the air-mouse and the real held-key press are impossible without root, while
// keys and text keep working because those go through Android's `input`
// command and never touch evdev. Measured on a Google ATV emulator image.
type caps struct {
	Pointer bool   `json:"pointer"` // air-mouse: relative motion, click, wheel
	HeldKey bool   `json:"heldKey"` // hold-OK -> app context menu
	Keys    bool   `json:"keys"`    // always true if we are serving at all
	Detail  string `json:"detail"`  // why, when something is unavailable
}

// probeCaps opens each evdev role once. Cheap, and the fd is kept for later use.
func probeCaps() caps {
	c := caps{Keys: true}
	var why []string
	if _, err := mo.fd(); err != nil {
		why = append(why, "pointer: "+firstLine(err.Error()))
	} else {
		c.Pointer = true
	}
	if _, err := kb.fd(); err != nil {
		why = append(why, "held-key: "+firstLine(err.Error()))
	} else {
		c.HeldKey = true
	}
	if len(why) > 0 {
		c.Detail = strings.Join(why, "; ") +
			" -- evdev is unavailable, which is expected on an SELinux-Enforcing box without root." +
			" Keys, text and app launch are unaffected."
	}
	return c
}

// firstLine keeps the device dump out of a one-line JSON field.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// describe renders the discovered devices for the log. Worth printing on every
// start: when the pointer lands on the wrong node, this is the only thing that
// makes it diagnosable without a shell on the box.
func describe(devs []inputDev) string {
	var b strings.Builder
	for _, d := range devs {
		fmt.Fprintf(&b, "\n  %-18s %-24q ev=%#x rel=%#x", d.node, d.name, d.ev, d.rel)
		switch {
		case d.isPointer():
			b.WriteString("  [pointer]")
		case d.isKeyboard():
			b.WriteString("  [keys]")
		}
	}
	return b.String()
}
