package webfetch

import (
	"bytes"
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
	"unicode/utf8"

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

// TestExecute_SummarizerError proves a summarizer failure returns the
// truncated page text with a warning prefix instead of failing the tool —
// the page content is still useful even when summarization isn't available.
func TestExecute_SummarizerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body><p>text</p></body></html>"))
	}))
	defer srv.Close()

	tl := New(&fakeSummarizer{err: errors.New("boom")})
	out, err := tl.Execute(t.Context(), args(t, map[string]any{"url": srv.URL, "question": "q"}))
	if err != nil {
		t.Fatalf("Execute() with a failing summarizer: want nil error, got %v", err)
	}
	if !strings.Contains(out, "[summary unavailable: boom]") {
		t.Errorf("Execute() = %q, want it to contain the summarizer-unavailable warning", out)
	}
	if !strings.Contains(out, "text") {
		t.Errorf("Execute() = %q, want it to still contain the page text", out)
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

// TestExecute_BodyCappedAtMaxBodyBytes proves a page far larger than the
// body cap doesn't get read in full — a 3 MiB page with a default 2 MiB
// cap must be truncated and the truncation noted in the result.
func TestExecute_BodyCappedAtMaxBodyBytes(t *testing.T) {
	const pageSize = 3 << 20 // 3 MiB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<p>"))
		_, _ = w.Write(bytes.Repeat([]byte("a"), pageSize))
	}))
	defer srv.Close()

	tl := New(nil, WithMaxBodyBytes(1<<20), WithMaxTextChars(2<<20)) // text cap kept above the body cap so it doesn't mask the body note
	out, err := tl.Execute(t.Context(), args(t, map[string]any{"url": srv.URL, "question": "q"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "[body truncated: read 1048576 bytes]") {
		t.Errorf("Execute() did not report body truncation; got suffix %q", out[max(0, len(out)-80):])
	}
	// The extracted text itself must also be well under the page size —
	// proves the body was actually capped, not just the note appended.
	if len(out) > 2<<20 {
		t.Errorf("Execute() len = %d, want it bounded near the 1 MiB body cap, not the 3 MiB page", len(out))
	}
}

// TestExecute_SummarizerInputCapped proves the text handed to the
// summarizer is bounded even for a long page, so it never overflows a
// model's context window.
func TestExecute_SummarizerInputCapped(t *testing.T) {
	longText := strings.Repeat("word ", 30000) // ~150000 chars
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, "<p>%s</p>", longText)
	}))
	defer srv.Close()

	fs := &fakeSummarizer{answer: "ok"}
	tl := New(fs, WithMaxSummaryChars(1000))
	_, err := tl.Execute(t.Context(), args(t, map[string]any{"url": srv.URL, "question": "q"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(fs.content) > 1000+100 { // small slack for the truncation note
		t.Errorf("summarizer input len = %d, want <= ~1100 (cap 1000 + note)", len(fs.content))
	}
	if !strings.Contains(fs.content, "[truncated:") {
		t.Errorf("summarizer input = %q, want a truncation note", fs.content)
	}
}

// TestExecute_NoSummarizerTextCapped proves the text returned verbatim
// (no summarizer configured) is bounded by maxTextChars.
func TestExecute_NoSummarizerTextCapped(t *testing.T) {
	longText := strings.Repeat("word ", 10000) // ~50000 chars
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, "<p>%s</p>", longText)
	}))
	defer srv.Close()

	tl := New(nil, WithMaxTextChars(500))
	out, err := tl.Execute(t.Context(), args(t, map[string]any{"url": srv.URL, "question": "q"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "[truncated: 500 of") {
		t.Errorf("Execute() = %q, want a truncation note naming the 500-char cap", out[:min(len(out), 60)])
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		max      int
		wantText string
		wantNote bool
	}{
		{"under cap: unchanged, no note", "hello", 10, "hello", false},
		{"exact cap: unchanged, no note", "hello", 5, "hello", false},
		{"over cap: truncated with note", "hello world", 5, "hello", true},
		{"multi-byte rune boundary respected", "héllo world", 2, "hé", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, note := truncateRunes(tt.in, tt.max)
			if text != tt.wantText {
				t.Errorf("truncateRunes(%q, %d) text = %q, want %q", tt.in, tt.max, text, tt.wantText)
			}
			if (note != "") != tt.wantNote {
				t.Errorf("truncateRunes(%q, %d) note = %q, want present=%v", tt.in, tt.max, note, tt.wantNote)
			}
			if !utf8.ValidString(text) {
				t.Errorf("truncateRunes(%q, %d) = %q, not valid UTF-8 (split a rune)", tt.in, tt.max, text)
			}
		})
	}
}

func TestWithMaxBodyBytesAndMaxCharsOptions(t *testing.T) {
	tl := New(nil, WithMaxBodyBytes(123), WithMaxSummaryChars(456), WithMaxTextChars(789))
	if tl.maxBodyBytes != 123 {
		t.Errorf("maxBodyBytes = %d, want 123", tl.maxBodyBytes)
	}
	if tl.maxSummaryChars != 456 {
		t.Errorf("maxSummaryChars = %d, want 456", tl.maxSummaryChars)
	}
	if tl.maxTextChars != 789 {
		t.Errorf("maxTextChars = %d, want 789", tl.maxTextChars)
	}

	tl2 := New(nil, WithMaxBodyBytes(0), WithMaxSummaryChars(-1), WithMaxTextChars(0))
	if tl2.maxBodyBytes != defaultMaxBodyBytes {
		t.Errorf("WithMaxBodyBytes(0): maxBodyBytes = %d, want default %d", tl2.maxBodyBytes, defaultMaxBodyBytes)
	}
	if tl2.maxSummaryChars != defaultMaxSummaryChars {
		t.Errorf("WithMaxSummaryChars(-1): maxSummaryChars = %d, want default %d", tl2.maxSummaryChars, defaultMaxSummaryChars)
	}
	if tl2.maxTextChars != defaultMaxTextChars {
		t.Errorf("WithMaxTextChars(0): maxTextChars = %d, want default %d", tl2.maxTextChars, defaultMaxTextChars)
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
