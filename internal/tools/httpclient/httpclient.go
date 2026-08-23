// Package httpclient provides the HTTP/1.1-forcing transport shared by
// webfetch and websearch.
//
// Both tools talk to endpoints that reject Go's default HTTP/2 client
// fingerprint (enterprise CDNs' WAFs match the h2 SETTINGS frame and
// return INTERNAL_ERROR). Forcing HTTP/1.1 avoids that fingerprint. The
// transport is cloned from http.DefaultTransport rather than built from
// scratch, so it keeps ProxyFromEnvironment (HTTPS_PROXY/HTTP_PROXY),
// dial/TLS handshake timeouts, and connection pooling.
package httpclient

import (
	"crypto/tls"
	"net/http"
)

// NewHTTP1Transport returns a transport that never negotiates HTTP/2.
func NewHTTP1Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	return t
}
