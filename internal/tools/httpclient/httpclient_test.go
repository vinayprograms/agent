package httpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHTTP1Transport_DisablesHTTP2(t *testing.T) {
	tr := NewHTTP1Transport()
	if tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 = true, want false")
	}
	if tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto = %v, want a non-nil empty map", tr.TLSNextProto)
	}
}

func TestNewHTTP1Transport_ClonesDefaultTransport(t *testing.T) {
	// Cloning from http.DefaultTransport (rather than building a bare
	// &http.Transport{}) keeps ProxyFromEnvironment, dial/TLS timeouts, and
	// idle connection pooling. DialContext is a distinguishing field the
	// zero-value transport never sets.
	tr := NewHTTP1Transport()
	want := http.DefaultTransport.(*http.Transport)
	if tr.Proxy == nil {
		t.Error("Proxy is nil, want the cloned ProxyFromEnvironment")
	}
	if tr.DialContext == nil {
		t.Error("DialContext is nil, want the cloned dialer")
	}
	if tr.IdleConnTimeout != want.IdleConnTimeout {
		t.Errorf("IdleConnTimeout = %v, want the cloned default %v", tr.IdleConnTimeout, want.IdleConnTimeout)
	}
}

// TestNewHTTP1Transport_HonoursHTTPSProxy proves the clone actually carries
// live proxy behavior, not just a non-nil func: pointing HTTPS_PROXY at a
// local server must route an HTTPS request through it as a CONNECT tunnel.
func TestNewHTTP1Transport_HonoursHTTPSProxy(t *testing.T) {
	connectSeen := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			connectSeen <- r.Host
		}
		w.WriteHeader(http.StatusBadGateway) // tunnel target is fake; seeing CONNECT is the assertion
	}))
	defer proxy.Close()

	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("NO_PROXY", "")

	tr := NewHTTP1Transport()
	client := &http.Client{Transport: tr}

	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/page", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm the transport itself resolves the proxy for this request
	// (this is what actually exercises the cloned ProxyFromEnvironment).
	proxyURL, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("tr.Proxy(req): %v", err)
	}
	if proxyURL == nil || proxyURL.String() != proxy.URL {
		t.Fatalf("tr.Proxy(req) = %v, want %v", proxyURL, proxy.URL)
	}

	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	select {
	case host := <-connectSeen:
		if host != "example.invalid:443" {
			t.Errorf("CONNECT host = %q, want %q", host, "example.invalid:443")
		}
	default:
		t.Fatal("proxy never received a CONNECT request")
	}
}
