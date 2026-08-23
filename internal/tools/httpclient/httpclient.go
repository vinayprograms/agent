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
	// Once anything in the process has used http.DefaultTransport, its
	// TLSClientConfig advertises "h2" in ALPN and the clone inherits that.
	// An empty TLSNextProto then leaves us with a server speaking HTTP/2
	// over a connection we only read as HTTP/1.1 ("malformed HTTP
	// response \x00\x00\x12\x04..."). Pin ALPN to http/1.1 explicitly.
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	} else {
		t.TLSClientConfig = t.TLSClientConfig.Clone()
	}
	t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return t
}
