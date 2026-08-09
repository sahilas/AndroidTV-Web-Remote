// Generating the TLS material and the auth token on the device itself.
//
// Why this exists: the material used to be made on the Mac by gen-cert.sh and
// pushed over adb. That works for a shell deploy, but not when the server is
// launched by the companion app -- an app cannot read /data/local/tmp/tvremote,
// where key.pem and token are 0600 shell. Loosening those, even briefly, puts a
// TLS private key and the auth token within reach of every app on the box, which
// is the exact bug this project already fixed once.
//
// Generating here removes the transfer entirely: the private key never crosses
// adb and never exists off the device. It also fixes a second problem for free --
// a DHCP address change used to invalidate the certificate's IP SAN until someone
// re-ran gen-cert.sh, and now the leaf is simply reissued when the address moves.
//
// The CA is created once and kept. Reissuing it would invalidate the trust the
// phone has already granted, which is the one thing here that costs a human a trip
// to the settings app.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	// Apple rejects a server certificate valid for more than 398 days, so the
	// leaf sits comfortably inside that. The CA is long-lived because replacing
	// it means re-trusting on every phone.
	leafDays = 380
	caYears  = 10
)

func pemBlock(path string, typ string, der []byte, mode os.FileMode) error {
	b := pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
	return os.WriteFile(path, b, mode)
}

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

// ensureToken creates the shared secret if absent. 16 bytes of crypto/rand, hex
// encoded -- the same shape deploy.sh produces, so a device provisioned either
// way is indistinguishable.
func ensureToken(dir string) error {
	p := filepath.Join(dir, "token")
	if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
		return nil
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(hex.EncodeToString(raw)), 0o600); err != nil {
		return err
	}
	log.Printf("provision: generated a new token in %s", p)
	return nil
}

// ensureCA loads the device CA, creating it on first run. Returns the parsed
// certificate and its key so a leaf can be signed against it.
func ensureCA(dir string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	if cb, err := os.ReadFile(certPath); err == nil {
		if kb, err2 := os.ReadFile(keyPath); err2 == nil {
			cblk, _ := pem.Decode(cb)
			kblk, _ := pem.Decode(kb)
			if cblk != nil && kblk != nil {
				ca, e1 := x509.ParseCertificate(cblk.Bytes)
				key, e2 := x509.ParsePKCS1PrivateKey(kblk.Bytes)
				// The key must actually belong to the cert. A box that once issued
				// its own CA and later had deploy.sh push a different ca.crt over
				// it keeps the old ca.key, and signing with that mismatched pair
				// fails deep inside x509 with "provided PrivateKey doesn't match
				// parent's PublicKey" -- an error nobody can act on.
				if e1 == nil && e2 == nil {
					if pub, ok := ca.PublicKey.(*rsa.PublicKey); ok && pub.Equal(&key.PublicKey) {
						return ca, key, nil
					}
					return nil, nil, fmt.Errorf("%s and %s are from different CAs "+
						"(ca.key does not match ca.crt); this box does not own the CA it is serving", certPath, keyPath)
				}
			}
		}
		// A CA cert with no usable key cannot sign anything. Say so rather than
		// silently making a second CA the phone has never trusted.
		return nil, nil, fmt.Errorf("%s exists but %s is missing or unreadable; "+
			"delete both to regenerate (every phone must then re-trust)", certPath, keyPath)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          sn,
		Subject:               pkix.Name{CommonName: "Android TV Remote Local CA"},
		NotBefore:             time.Now().Add(-time.Hour), // tolerate a skewed clock
		NotAfter:              time.Now().AddDate(caYears, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	if err := pemBlock(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, err
	}
	if err := pemBlock(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key), 0o600); err != nil {
		return nil, nil, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	log.Printf("provision: generated a device CA in %s (trust this on the phone)", certPath)
	return ca, key, nil
}

// leafUsable reports whether the leaf on disk can serve this address right now.
// Deliberately does NOT check who signed it: the question is only whether a
// client connecting today gets a valid certificate.
func leafUsable(certPath, keyPath string, ip net.IP, host string) (bool, string) {
	if _, err := os.Stat(keyPath); err != nil {
		return false, ""
	}
	leaf := readLeaf(certPath)
	if leaf == nil {
		return false, ""
	}
	if time.Now().After(leaf.NotAfter) {
		return false, fmt.Sprintf("leaf expired on %s", leaf.NotAfter.Format(time.DateOnly))
	}
	if ip != nil {
		ok := false
		for _, got := range leaf.IPAddresses {
			if got.Equal(ip) {
				ok = true
				break
			}
		}
		if !ok {
			return false, fmt.Sprintf("leaf does not cover %s (has %v) -- the box's address probably moved", ip, leaf.IPAddresses)
		}
	}
	if host != "" {
		ok := false
		for _, d := range leaf.DNSNames {
			if d == host {
				ok = true
				break
			}
		}
		if !ok {
			return false, fmt.Sprintf("leaf does not cover %s (has %v)", host, leaf.DNSNames)
		}
	}
	return true, ""
}

func readLeaf(certPath string) *x509.Certificate {
	b, err := os.ReadFile(certPath)
	if err != nil {
		return nil
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil
	}
	leaf, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil
	}
	return leaf
}

// leafIsForeign reports whether an existing leaf was signed by someone other than
// the CA we hold the key for.
func leafIsForeign(certPath string, ca *x509.Certificate) (bool, string) {
	leaf := readLeaf(certPath)
	if leaf == nil {
		return false, ""
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		return true, leaf.Issuer.CommonName
	}
	return false, ""
}

// ensureTLS makes sure dir holds a cert.pem/key.pem this box can serve with.
//
// cert.pem is the fullchain, leaf first: iOS will not build a path to a CA it has
// installed unless the intermediate chain is presented, and serving the leaf alone
// is the classic "works on desktop, Not Secure on iPhone" failure.
func ensureTLS(dir, advIP, mdnsHost string) error {
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	ip := net.ParseIP(advIP)
	host := ""
	if mdnsHost != "" {
		host = mdnsHost + ".local"
	}

	// A certificate pushed by deploy.sh is signed by the Mac's CA, which the
	// phone already trusts. Reissuing over it with a locally generated CA would
	// silently break that trust, so an existing usable leaf is never touched --
	// whoever issued it.
	if usable, why := leafUsable(certPath, keyPath, ip, host); usable {
		return nil
	} else if why != "" {
		log.Printf("provision: %s", why)
	}

	ca, caKey, err := ensureCA(dir)
	if err != nil {
		// Cannot issue without a CA we own. The existing certificate is left
		// exactly as it is -- serving a stale cert is recoverable by re-running
		// gen-cert.sh, silently swapping the trust anchor is not.
		if readLeaf(certPath) != nil {
			log.Printf("provision: %v -- leaving the existing certificate alone. "+
				"Re-run ./gen-cert.sh && ./deploy.sh, or delete cert.pem key.pem ca.crt ca.key "+
				"to let this box issue its own (every phone must then re-trust).", err)
			return nil
		}
		return err
	}
	// Same reasoning in the stale case: if the leaf on disk was NOT signed by the
	// CA we hold, we cannot reissue it without changing the trust anchor. Say so
	// and leave it, rather than quietly making every phone show "Not Secure".
	if foreign, issuer := leafIsForeign(certPath, ca); foreign {
		log.Printf("provision: %s exists, issued by %q, and no longer covers this address. "+
			"NOT replacing it -- that would change the CA your phone trusts. "+
			"Re-run ./gen-cert.sh && ./deploy.sh, or delete cert.pem/key.pem to let this box issue its own.",
			certPath, issuer)
		return nil
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	sn, err := serial()
	if err != nil {
		return err
	}
	cn := advIP
	if cn == "" {
		cn = host
	}
	if cn == "" {
		cn = "android-tv-remote"
	}
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, leafDays),
		// CA:FALSE and serverAuth are both load-bearing for iOS: it rejects a
		// self-signed CA:TRUE certificate used as a server leaf outright.
		IsCA:                  false,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	// Loopback is always included so a forwarded port (adb forward, used when the
	// box has no reachable LAN address) validates without -k.
	tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP("127.0.0.1"))
	if ip != nil && !ip.IsLoopback() {
		tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
	}
	if host != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, host)
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	// Fullchain: leaf then CA.
	chain := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})...,
	)
	if err := os.WriteFile(certPath, chain, 0o644); err != nil {
		return err
	}
	if err := pemBlock(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key), 0o600); err != nil {
		return err
	}
	log.Printf("provision: issued a leaf for %v (%v), valid %d days", tmpl.IPAddresses, tmpl.DNSNames, leafDays)
	return nil
}

// provision brings dir up to a servable state. Safe to call on every start: it
// does nothing when the material is already present and still correct.
func provision(dir, advIP, mdnsHost string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := ensureToken(dir); err != nil {
		return fmt.Errorf("token: %w", err)
	}
	if err := ensureTLS(dir, advIP, mdnsHost); err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	return nil
}
