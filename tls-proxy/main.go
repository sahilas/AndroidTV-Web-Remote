// Tiny HTTPS reverse proxy for the projector remote.
// Terminates TLS on :8443 and forwards to the busybox httpd on 127.0.0.1:8790.
// Static binary, no deps. Cross-compile: GOOS=linux GOARCH=arm GOARM=7 go build.
package main

import (
	"crypto/tls"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

const (
	listen  = ":8443"
	backend = "http://127.0.0.1:8790"
	dir     = "/data/local/tmp/tvremote"
)

func main() {
	b, err := url.Parse(backend)
	if err != nil {
		log.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(b)
	srv := &http.Server{
		Addr:      listen,
		Handler:   proxy,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	log.Printf("HTTPS proxy %s -> %s", listen, backend)
	log.Fatal(srv.ListenAndServeTLS(dir+"/cert.pem", dir+"/key.pem"))
}
