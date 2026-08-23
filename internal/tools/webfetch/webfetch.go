// Package webfetch provides an in-tree replacement for agentkit's built-in
// web_fetch tool.
//
// It exists because agentkit's client (http.Client with only a timeout set)
// negotiates HTTP/2 via ALPN, and enterprise CDNs (Akamai, Cloudflare)
// fingerprint Go's h2 SETTINGS frame and reject the connection with
// INTERNAL_ERROR. This implementation forces HTTP/1.1 and sends
// browser-like headers, which those CDNs tolerate. agentkit's WebOption
// (tools.WithHTTPTimeout) has no hook to swap the transport, so this tool
// is registered INSTEAD of tools.Fetch, never over it:
//
//	reg.Register(tools.New(webfetch.New(summarizer, opts...)))
//
// The tool reports Name() == "web_fetch" and accepts the same arguments
// ("url", "question") and returns the same output (full extracted text
// when summarizer is nil, else the summarizer's answer; HTTP errors with a
// body are returned as a result, not a Go error) as the built-in, so
// prompts written against the built-in keep working.
package webfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vinayprograms/agent/internal/tools/httpclient"
	"github.com/vinayprograms/agentkit/tools"
)

// browserUserAgent is a realistic desktop browser UA — CDNs that fingerprint
// Go's default "Go-http-client" or a bot-identifying UA block the request
// before it reaches the origin.
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

const defaultHTTPTimeout = 2 * time.Minute

// Default caps on how much of a fetched page this tool reads and hands
// onward. Unbounded reads/summarizer inputs against large pages blow the
// summarizer's context window (400 "prompt too long") or overrun the
// caller's fetch timeout; these keep both bounded regardless of page size.
const (
	defaultMaxBodyBytes    = 2 << 20 // 2 MiB, raw HTTP response body
	defaultMaxSummaryChars = 60000   // ~15k tokens, well under any 128k model
	defaultMaxTextChars    = 15000   // returned verbatim (no summarizer, or summarizer failed)
)

// Tool implements agentkit's tools.Tool interface for web_fetch, using an
// HTTP/1.1-only client to avoid HTTP/2 fingerprint rejection.
type Tool struct {
	summarizer tools.Summarizer
	client     *http.Client

	maxBodyBytes    int64
	maxSummaryChars int
	maxTextChars    int
}

// transport lets tests point Execute's client at a custom RoundTripper
// (e.g. one trusting an httptest TLS server's certificate) while keeping
// New's production default (httpclient.NewHTTP1Transport()) unexported.
func withTransport(rt http.RoundTripper) Option {
	return func(t *Tool) { t.client.Transport = rt }
}

var _ tools.Tool = (*Tool)(nil)

// Option configures a Tool.
type Option func(*Tool)

// WithHTTPTimeout sets the HTTP client timeout (default 2 minutes). A
// non-positive value leaves the default in place.
func WithHTTPTimeout(d time.Duration) Option {
	return func(t *Tool) {
		if d > 0 {
			t.client.Timeout = d
		}
	}
}

// WithMaxBodyBytes caps how much of the raw HTTP response body is read
// (default 2 MiB). A non-positive value leaves the default in place.
func WithMaxBodyBytes(n int64) Option {
	return func(t *Tool) {
		if n > 0 {
			t.maxBodyBytes = n
		}
	}
}

// WithMaxSummaryChars caps how many characters of extracted text are handed
// to the summarizer (default 60000). A non-positive value leaves the
// default in place.
func WithMaxSummaryChars(n int) Option {
	return func(t *Tool) {
		if n > 0 {
			t.maxSummaryChars = n
		}
	}
}

// WithMaxTextChars caps how many characters of extracted text are returned
// verbatim — when there is no summarizer, or the summarizer fails (default
// 15000). A non-positive value leaves the default in place.
func WithMaxTextChars(n int) Option {
	return func(t *Tool) {
		if n > 0 {
			t.maxTextChars = n
		}
	}
}

// New constructs the replacement web_fetch tool. summarizer may be nil, in
// which case Execute returns the full extracted page text.
func New(summarizer tools.Summarizer, opts ...Option) *Tool {
	t := &Tool{
		summarizer:      summarizer,
		client:          &http.Client{Timeout: defaultHTTPTimeout, Transport: httpclient.NewHTTP1Transport()},
		maxBodyBytes:    defaultMaxBodyBytes,
		maxSummaryChars: defaultMaxSummaryChars,
		maxTextChars:    defaultMaxTextChars,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *Tool) Name() string { return "web_fetch" }

func (t *Tool) Description() string {
	return "Fetch and summarize content from a URL. Requires a question/prompt — the tool returns a concise answer based on the page content, not the raw page. Use after web_search to get specific information from promising results."
}

func (t *Tool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"url": {
			Type:        tools.StringParam,
			Description: "URL to fetch (typically from web_search results)",
			Required:    true,
		},
		"question": {
			Type:        tools.StringParam,
			Description: "What information to extract from the page",
			Required:    true,
		},
	}
}

func (t *Tool) Execute(ctx context.Context, args tools.Args) (string, error) {
	url, err := args.String("url")
	if err != nil {
		return "", err
	}
	question, err := args.String("question")
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	// Mimic a real browser request to pass WAF/CDN checks. Accept-Encoding
	// is intentionally omitted — Go decompresses gzip transparently only
	// when it adds the header itself.
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("DNT", "1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	// Read one byte past the cap so a body that exactly fills it can be
	// told apart from one that overflows it.
	body, err := io.ReadAll(io.LimitReader(resp.Body, t.maxBodyBytes+1))
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	bodyTruncated := int64(len(body)) > t.maxBodyBytes
	if bodyTruncated {
		body = body[:t.maxBodyBytes]
	}

	// HTTP errors with a body are results, not errors.
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, resp.Status, string(body)), nil
	}

	content := extractReadableText(string(body))
	if bodyTruncated {
		content += fmt.Sprintf("\n\n[body truncated: read %d bytes]", t.maxBodyBytes)
	}

	if t.summarizer == nil {
		text, truncated := truncateRunes(content, t.maxTextChars)
		return text + truncated, nil
	}

	summaryInput, truncated := truncateRunes(content, t.maxSummaryChars)
	answer, err := t.summarizer.Summarize(ctx, summaryInput+truncated, question)
	if err == nil && strings.TrimSpace(answer) == "" {
		// Some models (reasoning models under a tight token budget) return
		// stop_reason="length" with all tokens spent on hidden thinking and
		// no error — an empty-but-successful answer. Treat it the same as
		// a summarizer failure so we still degrade to raw text instead of
		// silently returning "".
		err = fmt.Errorf("summarizer returned empty answer")
	}
	if err != nil {
		text, textTruncated := truncateRunes(content, t.maxTextChars)
		return fmt.Sprintf("[summary unavailable: %v]\n\n%s%s", err, text, textTruncated), nil
	}
	return answer, nil
}

// truncateRunes cuts s to at most max runes (never splitting a multi-byte
// rune) and, when it truncated, returns a trailing note reporting how many
// of the original characters were dropped. The empty string is returned as
// the note when no truncation was needed.
func truncateRunes(s string, max int) (text, note string) {
	runes := []rune(s)
	if len(runes) <= max {
		return s, ""
	}
	kept := string(runes[:max])
	return kept, fmt.Sprintf("\n\n[truncated: %d of %d characters]", max, len(runes))
}

var (
	reScript       = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle        = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reHead         = regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
	reNav          = regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`)
	reFooter       = regexp.MustCompile(`(?is)<footer[^>]*>.*?</footer>`)
	reComments     = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBlock        = regexp.MustCompile(`<(p|div|br|h[1-6]|li|tr)[^>]*>`)
	reTags         = regexp.MustCompile(`<[^>]+>`)
	reMultiSpace   = regexp.MustCompile(`[ \t]+`)
	reMultiNewline = regexp.MustCompile(`\n{3,}`)
)

// extractReadableText removes HTML tags and extracts readable content,
// matching agentkit's built-in web_fetch output format exactly.
func extractReadableText(html string) string {
	html = reScript.ReplaceAllString(html, "")
	html = reStyle.ReplaceAllString(html, "")
	html = reHead.ReplaceAllString(html, "")
	html = reNav.ReplaceAllString(html, "")
	html = reFooter.ReplaceAllString(html, "")
	html = reComments.ReplaceAllString(html, "")
	html = reBlock.ReplaceAllString(html, "\n")
	text := reTags.ReplaceAllString(html, "")

	// Only decode non-printing entities. Keep &amp; &lt; &gt; etc. escaped
	// to avoid reintroducing characters that could be injection payloads.
	text = strings.ReplaceAll(text, "&nbsp;", " ")

	text = reMultiSpace.ReplaceAllString(text, " ")
	text = reMultiNewline.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
