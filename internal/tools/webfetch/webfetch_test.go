package webfetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/tools/httpclient"
	"github.com/vinayprograms/agentkit/tools"
)

func args(t *testing.T, raw map[string]any) tools.Args {
	t.Helper()
	a, err := tools.Validate((*Tool)(nil).Parameters(), raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return a
}

func TestNameDescriptionParameters(t *testing.T) {
	tl := New(nil)
	if got, want := tl.Name(), "web_fetch"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if tl.Description() == "" {
		t.Error("Description() is empty")
	}
	params := tl.Parameters()
	for _, key := range []string{"url", "question"} {
		p, ok := params[key]
		if !ok {
			t.Fatalf("Parameters() missing %q", key)
		}
		if !p.Required {
			t.Errorf("Parameters()[%q].Required = false, want true", key)
		}
	}
}

func TestExecute_SendsBrowserHeaders(t *testing.T) {
	var captured *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body><p>Hello world</p></body></html>"))
	}))
	defer srv.Close()

	tl := New(nil)
	out, err := tl.Execute(t.Context(), args(t, map[string]any{"url": srv.URL, "question": "what is here?"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Hello world") {
		t.Errorf("Execute() = %q, want it to contain %q", out, "Hello world")
	}

	if captured == nil {
		t.Fatal("handler was not invoked")
	}
	wantHeaders := map[string]string{
		"User-Agent":                browserUserAgent,
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"Accept-Language":           "en-US,en;q=0.9",
		"Dnt":                       "1",
		"Upgrade-Insecure-Requests": "1",
	}
	for k, want := range wantHeaders {
		if got := captured.Header.Get(k); got != want {
			t.Errorf("header %q = %q, want %q", k, got, want)
		}
	}
}

// TestExecute_ForcesHTTP1EvenAgainstHTTP2Server proves the transport really
// disables HTTP/2 negotiation, not just that a plain-HTTP test server
// happens to speak HTTP/1.1. It spins up a TLS server that offers h2 via
// ALPN, first confirms a default (HTTP/2-capable) client actually gets h2
// from it — so the test can't pass vacuously against a server that never
// offered h2 — then confirms New's client gets h1.
func TestExecute_ForcesHTTP1EvenAgainstHTTP2Server(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, "protomajor=%d", r.ProtoMajor)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	// srv.Client() is httptest's own helper: it trusts the server's
	// self-signed cert and, since EnableHTTP2 is set, is wired for h2.
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("sanity GET: %v", err)
	}
	resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("sanity check: srv.Client() got ProtoMajor=%d, want 2 (test server isn't actually offering h2, so this test can't prove anything)", resp.ProtoMajor)
	}

	certPool := x509.NewCertPool()
	certPool.AddCert(srv.Certificate())
	forced := httpclient.NewHTTP1Transport()
	forced.TLSClientConfig = &tls.Config{RootCAs: certPool}

	tl := New(nil, withTransport(forced))
	out, err := tl.Execute(t.Context(), args(t, map[string]any{"url": srv.URL, "question": "q"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "protomajor=1" {
		t.Errorf("Execute() = %q, want %q (the server should have seen HTTP/1.1)", out, "protomajor=1")
	}
}

// optionalArgs builds Args via Validate with both params marked optional, so
// a genuinely missing key reaches Execute's own Args.String error handling
// instead of being rejected by Validate first.
func optionalArgs(t *testing.T, raw map[string]any) tools.Args {
	t.Helper()
	params := map[string]tools.Param{
		"url":      {Type: tools.StringParam},
		"question": {Type: tools.StringParam},
	}
	a, err := tools.Validate(params, raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return a
}

func TestExecute_MissingArgs(t *testing.T) {
	tl := New(nil)
	if _, err := tl.Execute(t.Context(), optionalArgs(t, map[string]any{"question": "q"})); err == nil {
		t.Error("Execute() with missing url: want error, got nil")
	}
	if _, err := tl.Execute(t.Context(), optionalArgs(t, map[string]any{"url": "http://example.com"})); err == nil {
		t.Error("Execute() with missing question: want error, got nil")
	}
}

func TestExecute_InvalidURL(t *testing.T) {
	tl := New(nil)
	_, err := tl.Execute(t.Context(), args(t, map[string]any{"url": "http://[::1", "question": "q"}))
	if err == nil {
		t.Error("Execute() with malformed url: want error, got nil")
	}
}

func TestExecute_ConnectionFailure(t *testing.T) {
	tl := New(nil, WithHTTPTimeout(50*time.Millisecond))
	_, err := tl.Execute(t.Context(), args(t, map[string]any{"url": "http://127.0.0.1:1", "question": "q"}))
	if err == nil {
		t.Error("Execute() against a closed port: want error, got nil")
	}
}

// errReader always fails, simulating a connection dropped mid-body.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read: connection reset") }
func (errReader) Close() error             { return nil }

// brokenBodyTransport returns a 200 response whose body errors on Read.
type brokenBodyTransport struct{}

func (brokenBodyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       errReader{},
		Header:     make(http.Header),
		Request:    r,
	}, nil
}

func TestExecute_BodyReadError(t *testing.T) {
	tl := New(nil)
	tl.client.Transport = brokenBodyTransport{}
	_, err := tl.Execute(t.Context(), args(t, map[string]any{"url": "http://example.invalid", "question": "q"}))
	if err == nil {
		t.Error("Execute() with a body read failure: want error, got nil")
	}
}

func TestExecute_HTTPErrorIsAResultNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("not found"))
	}))
	defer srv.Close()

	tl := New(nil)
	out, err := tl.Execute(t.Context(), args(t, map[string]any{"url": srv.URL, "question": "q"}))
	if err != nil {
		t.Fatalf("Execute() with HTTP 404: want nil error, got %v", err)
	}
	if !strings.Contains(out, "HTTP 404") || !strings.Contains(out, "not found") {
		t.Errorf("Execute() = %q, want it to describe the HTTP 404", out)
	}
}

// fakeSummarizer records the content/question it was given and returns a
// canned answer, or an error when err is set.
type fakeSummarizer struct {
	answer  string
	err     error
	content string
	q       string
}

func (f *fakeSummarizer) Summarize(_ context.Context, content, question string) (string, error) {
	f.content, f.q = content, question
	if f.err != nil {
		return "", f.err
	}
	return f.answer, nil
}

func TestExecute_WithSummarizer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body><p>Some page text</p></body></html>"))
	}))
	defer srv.Close()

	fs := &fakeSummarizer{answer: "the answer"}
	tl := New(fs)
	out, err := tl.Execute(t.Context(), args(t, map[string]any{"url": srv.URL, "question": "what?"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "the answer" {
		t.Errorf("Execute() = %q, want %q", out, "the answer")
	}
	if fs.q != "what?" {
		t.Errorf("Summarize question = %q, want %q", fs.q, "what?")
	}
	if !strings.Contains(fs.content, "Some page text") {
		t.Errorf("Summarize content = %q, want it to contain %q", fs.content, "Some page text")
	}
}

func TestExecute_SummarizerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body><p>text</p></body></html>"))
	}))
	defer srv.Close()

	tl := New(&fakeSummarizer{err: errors.New("boom")})
	_, err := tl.Execute(t.Context(), args(t, map[string]any{"url": srv.URL, "question": "q"}))
	if err == nil {
		t.Error("Execute() with a failing summarizer: want error, got nil")
	}
}

func TestExtractReadableText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips script and style", `<html><head><style>.a{}</style></head><body><script>evil()</script><p>Hi</p></body></html>`, "Hi"},
		{"strips nav and footer", `<nav>menu</nav><p>Body</p><footer>foot</footer>`, "Body"},
		{"strips comments", `<!-- hidden --><p>Visible</p>`, "Visible"},
		{"decodes nbsp only", `<p>a&nbsp;b&amp;c</p>`, "a b&amp;c"},
		{"collapses whitespace and blank lines", "<p>a</p>\n\n\n\n<p>b</p>", "a\n\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractReadableText(tt.in); got != tt.want {
				t.Errorf("extractReadableText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestWithHTTPTimeout(t *testing.T) {
	tl := New(nil, WithHTTPTimeout(5*time.Second))
	if tl.client.Timeout != 5*time.Second {
		t.Errorf("client.Timeout = %v, want 5s", tl.client.Timeout)
	}

	tl2 := New(nil, WithHTTPTimeout(0))
	if tl2.client.Timeout != defaultHTTPTimeout {
		t.Errorf("WithHTTPTimeout(0): client.Timeout = %v, want default %v", tl2.client.Timeout, defaultHTTPTimeout)
	}
}
