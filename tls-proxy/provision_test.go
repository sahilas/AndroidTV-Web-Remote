package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// A box with nothing staged must become servable on its own. This is what makes
// the companion-app path possible at all: an app cannot read the shell-owned
// staging directory, so nothing can be handed to it.
func TestProvisionFromEmpty(t *testing.T) {
	d := t.TempDir()
	if err := provision(d, "192.168.7.7", "androidtvremote"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	for _, f := range []string{"token", "ca.crt", "ca.key", "cert.pem", "key.pem"} {
		if _, err := os.Stat(filepath.Join(d, f)); err != nil {
			t.Errorf("%s not created: %v", f, err)
		}
	}
	// Secrets must not be world-readable. This is the property the whole
	// provisioning change exists to protect.
	for _, f := range []string{"key.pem", "ca.key", "token"} {
		fi, err := os.Stat(filepath.Join(d, f))
		if err != nil {
			continue
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is mode %o, want no group/other access", f, fi.Mode().Perm())
		}
	}
}

// Running twice must not reissue anything: a new leaf every boot would churn the
// certificate and, worse, a new CA would break the phone's trust.
func TestProvisionIsIdempotent(t *testing.T) {
	d := t.TempDir()
	if err := provision(d, "192.168.7.7", "androidtvremote"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(d, "cert.pem"))
	tok1, _ := os.ReadFile(filepath.Join(d, "token"))
	if err := provision(d, "192.168.7.7", "androidtvremote"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(d, "cert.pem"))
	tok2, _ := os.ReadFile(filepath.Join(d, "token"))
	if string(before) != string(after) {
		t.Error("certificate was reissued on an unchanged box")
	}
	if string(tok1) != string(tok2) {
		t.Error("token was regenerated -- every phone's saved shortcut would break")
	}
}

// The DHCP fix: when the address moves the leaf is reissued, but the CA must
// survive, because replacing it is what forces a human back into the phone's
// settings.
func TestLeafReissuedOnAddressChangeButCASurvives(t *testing.T) {
	d := t.TempDir()
	if err := provision(d, "192.168.7.7", "androidtvremote"); err != nil {
		t.Fatal(err)
	}
	ca1, _ := os.ReadFile(filepath.Join(d, "ca.crt"))
	leaf1, _ := os.ReadFile(filepath.Join(d, "cert.pem"))

	if err := provision(d, "10.1.2.3", "androidtvremote"); err != nil {
		t.Fatal(err)
	}
	ca2, _ := os.ReadFile(filepath.Join(d, "ca.crt"))
	leaf2, _ := os.ReadFile(filepath.Join(d, "cert.pem"))

	if string(ca1) != string(ca2) {
		t.Error("CA changed on an address move -- every phone would have to re-trust")
	}
	if string(leaf1) == string(leaf2) {
		t.Error("leaf was NOT reissued for the new address; TLS would fail on the new IP")
	}
	l := readLeaf(filepath.Join(d, "cert.pem"))
	if l == nil {
		t.Fatal("no leaf")
	}
	found := false
	for _, ip := range l.IPAddresses {
		if ip.Equal(net.ParseIP("10.1.2.3")) {
			found = true
		}
	}
	if !found {
		t.Errorf("reissued leaf does not cover the new address: %v", l.IPAddresses)
	}
}

// A certificate pushed by deploy.sh is signed by a CA the phone already trusts.
// Overwriting it would silently break that trust, which is far worse than serving
// a stale certificate the user can fix by re-running gen-cert.sh.
func TestPushedCertificateIsNeverClobbered(t *testing.T) {
	d := t.TempDir()
	// box issues its own first
	if err := provision(d, "192.168.7.7", "androidtvremote"); err != nil {
		t.Fatal(err)
	}
	// then a deploy pushes foreign material over it, leaving the old ca.key behind
	foreign := t.TempDir()
	if err := provision(foreign, "10.9.9.9", "otherhost"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"cert.pem", "key.pem", "ca.crt"} {
		b, _ := os.ReadFile(filepath.Join(foreign, f))
		os.WriteFile(filepath.Join(d, f), b, 0o600)
	}
	pushed, _ := os.ReadFile(filepath.Join(d, "cert.pem"))

	// stale for this address, and the CA key on disk no longer matches ca.crt
	if err := provision(d, "172.16.0.1", "androidtvremote"); err != nil {
		t.Fatalf("must not fail hard: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(d, "cert.pem"))
	if string(pushed) != string(got) {
		t.Error("a pushed certificate was replaced; the phone's trusted CA would no longer match")
	}
}
