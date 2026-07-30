// HTTPS reverse proxy for the projector remote.
// Terminates TLS on :8443 -> busybox httpd on 127.0.0.1:8790.
// Also 301-redirects a plaintext HTTP request that lands on :8443 to https://,
// so "forgot the s in https" self-corrects instead of showing a raw TLS error.
// Static binary, no deps. Cross-compile: GOOS=linux GOARCH=arm GOARM=7 go build.
package main

import (
	"bufio"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
)

const (
	listen  = ":8443"
	backend = "http://127.0.0.1:8790"
	dir     = "/data/local/tmp/tvremote"
	defHost = "192.168.220.53:8443"
)

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
// requests are handled inline with a 301 to https.
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
		go redirect(fc) // plaintext HTTP -> https
	}
}

func redirect(c net.Conn) {
	defer c.Close()
	host, path := defHost, "/"
	if req, err := http.ReadRequest(bufio.NewReader(c)); err == nil {
		if req.Host != "" {
			host = req.Host
		}
		path = req.URL.RequestURI()
	}
	io.WriteString(c, "HTTP/1.1 301 Moved Permanently\r\nLocation: https://"+host+path+
		"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
}

func main() {
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
	srv := &http.Server{Handler: httputil.NewSingleHostReverseProxy(b)}
	tlsLn := tls.NewListener(splitListener{ln}, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	log.Printf("HTTPS %s -> %s (plaintext auto-redirects)", listen, backend)
	log.Fatal(srv.Serve(tlsLn))
}
