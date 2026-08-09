package main

import "testing"

// Verbatim /proc/bus/input/devices from the HiSilicon projector. Five devices,
// two of which qualify as key injectors and one as a pointer -- which is exactly
// the ambiguity the classifier has to resolve the same way every boot.
const projectorDevices = `I: Bus=0000 Vendor=0001 Product=0001 Version=0100
N: Name="Hi keyboard"
P: Phys=
S: Sysfs=/devices/virtual/input/input0
U: Uniq=
H: Handlers=kbd event0
B: PROP=0
B: EV=b
B: KEY=7fffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff fffffffe
B: ABS=600000 0

I: Bus=0000 Vendor=0001 Product=0002 Version=0100
N: Name="Hi mouse"
P: Phys=
S: Sysfs=/devices/virtual/input/input1
U: Uniq=
H: Handlers=mouse0 event1
B: PROP=0
B: EV=7
B: KEY=1f0000 0 0 0 0 0 0 0 0
B: REL=103

I: Bus=0000 Vendor=0000 Product=0000 Version=0100
N: Name="qwerty"
P: Phys=
S: Sysfs=/devices/virtual/input/input2
U: Uniq=
H: Handlers=mouse1 event2
B: PROP=0
B: EV=b
B: KEY=400 0 0 0 0 0 0 0 0 0 0
B: ABS=2658000 11000003

I: Bus=0003 Vendor=32e6 Product=9005 Version=0301
N: Name="HD camera : HD camera "
P: Phys=usb-f9890000.ehci-2/button
S: Sysfs=/devices/platform/soc/f9890000.ehci/usb1/1-2/1-2:1.0/input/input3
U: Uniq=
H: Handlers=kbd event3
B: PROP=0
B: EV=3
B: KEY=100000 0 0 0 0 0 0

I: Bus=0006 Vendor=046d Product=0002 Version=0000
N: Name="Hi Keypad"
P: Phys=
S: Sysfs=/devices/virtual/input/input4
U: Uniq=
H: Handlers=kbd event4
B: PROP=0
B: EV=3
B: KEY=7fffffff ffffffff ffffffff ffffffff ffffffff ffffffff ffffffff fffffffe
`

func TestParseInputDevices(t *testing.T) {
	devs := parseInputDevices(projectorDevices)
	if len(devs) != 5 {
		t.Fatalf("parsed %d devices, want 5: %+v", len(devs), devs)
	}
	for i, want := range []struct {
		name, node string
	}{
		{"Hi keyboard", "/dev/input/event0"},
		{"Hi mouse", "/dev/input/event1"},
		{"qwerty", "/dev/input/event2"},
		{"HD camera : HD camera ", "/dev/input/event3"},
		{"Hi Keypad", "/dev/input/event4"},
	} {
		if devs[i].name != want.name || devs[i].node != want.node {
			t.Errorf("device %d = %q/%s, want %q/%s", i, devs[i].name, devs[i].node, want.name, want.node)
		}
	}
}

// The whole point of the rewrite: the right nodes get chosen without anyone
// naming them. These must match what the hardcoded "Hi mouse"/"Hi keyboard"
// lookup resolved to, or this is a regression dressed up as portability.
func TestCapabilityDetectionMatchesTheOldHardcodedNodes(t *testing.T) {
	devs := parseInputDevices(projectorDevices)

	p, err := pickNode(devs, "", inputDev.isPointer)
	if err != nil {
		t.Fatalf("no pointer found: %v", err)
	}
	if p.node != "/dev/input/event1" || p.name != "Hi mouse" {
		t.Errorf("pointer = %s (%q), want /dev/input/event1 (Hi mouse)", p.node, p.name)
	}

	k, err := pickNode(devs, "", inputDev.isKeyboard)
	if err != nil {
		t.Fatalf("no key device found: %v", err)
	}
	if k.node != "/dev/input/event0" || k.name != "Hi keyboard" {
		t.Errorf("keys = %s (%q), want /dev/input/event0 (Hi keyboard)", k.node, k.name)
	}
}

func TestClassification(t *testing.T) {
	devs := parseInputDevices(projectorDevices)
	byName := map[string]inputDev{}
	for _, d := range devs {
		byName[d.name] = d
	}

	for _, c := range []struct {
		name              string
		pointer, keyboard bool
	}{
		// EV=7 (SYN|KEY|REL), REL=103 -> REL_X|REL_Y|REL_WHEEL
		{"Hi mouse", true, false},
		// EV=b (SYN|KEY|ABS), KEY low word fffffffe has bit 28 (KEY_ENTER)
		{"Hi keyboard", false, true},
		// also a full keymap: a genuine second candidate, not a bug
		{"Hi Keypad", false, true},
		// KEY low word is 0 -- no KEY_ENTER, so not a key injector
		{"qwerty", false, false},
		{"HD camera : HD camera ", false, false},
	} {
		d := byName[c.name]
		if got := d.isPointer(); got != c.pointer {
			t.Errorf("%q isPointer = %v, want %v (ev=%#x rel=%#x)", c.name, got, c.pointer, d.ev, d.rel)
		}
		if got := d.isKeyboard(); got != c.keyboard {
			t.Errorf("%q isKeyboard = %v, want %v (ev=%#x key=%#x)", c.name, got, c.keyboard, d.ev, d.key)
		}
	}
}

// lowWord takes the LAST field, because the kernel prints these high word first.
// Taking the first would read a different bit range entirely and misclassify
// every device on a box whose bitmaps span more than one word.
func TestLowWord(t *testing.T) {
	for _, c := range []struct {
		in   string
		want uint64
	}{
		{"7", 0x7},
		{"1f0000 0 0 0 0 0 0 0 0", 0}, // low word is the trailing 0
		{"7fffffff ffffffff fffffffe", 0xfffffffe},
		{"", 0},
		{"zzz", 0},
	} {
		if got := lowWord(c.in); got != c.want {
			t.Errorf("lowWord(%q) = %#x, want %#x", c.in, got, c.want)
		}
	}
}

// A name override is the escape hatch for a box where detection guesses wrong.
// It must fail loudly rather than silently falling back to autodetection --
// otherwise pinning a name looks like it worked while doing nothing.
func TestPreferNameOverride(t *testing.T) {
	devs := parseInputDevices(projectorDevices)

	d, err := pickNode(devs, "Hi Keypad", inputDev.isKeyboard)
	if err != nil {
		t.Fatalf("explicit name rejected: %v", err)
	}
	if d.node != "/dev/input/event4" {
		t.Errorf("got %s, want /dev/input/event4", d.node)
	}

	if _, err := pickNode(devs, "No Such Device", inputDev.isKeyboard); err == nil {
		t.Error("a name that does not exist must be an error, not a silent fallback")
	}
	if _, err := pickNode(devs, "qwerty", inputDev.isKeyboard); err == nil {
		t.Error("a named device lacking the capability must be an error")
	}
}

// A box with no usable device must say so. Returning a zero-value inputDev would
// make the caller open "" and report a confusing open error instead.
func TestNoCandidates(t *testing.T) {
	devs := parseInputDevices(`I: Bus=0003 Vendor=1 Product=1 Version=1
N: Name="power button"
H: Handlers=kbd event0
B: EV=3
B: KEY=10 0 0 0 0
`)
	if _, err := pickNode(devs, "", inputDev.isPointer); err == nil {
		t.Error("expected an error when no pointer exists")
	}
	if _, err := pickNode(devs, "", inputDev.isKeyboard); err == nil {
		t.Error("expected an error when no key device exists")
	}
}
