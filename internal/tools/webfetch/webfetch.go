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
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vinayprograms/agentkit/tools"
)

// browserUserAgent is a realistic desktop browser UA — CDNs that fingerprint
// Go's default "Go-http-client" or a bot-identifying UA block the request
// before it reaches the origin.
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

const defaultHTTPTimeout = 2 * time.Minute

// Tool implements agentkit's tools.Tool interface for web_fetch, using an
// HTTP/1.1-only client to avoid HTTP/2 fingerprint rejection.
type Tool struct {
	summarizer tools.Summarizer
	client     *http.Client
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

// New constructs the replacement web_fetch tool. summarizer may be nil, in
// which case Execute returns the full extracted page text.
func New(summarizer tools.Summarizer, opts ...Option) *Tool {
	// Disable HTTP/2 ALPN negotiation — Go's h2 SETTINGS fingerprint is
	// trivially identifiable and causes INTERNAL_ERROR from CDN WAFs.
	transport := &http.Transport{
		ForceAttemptHTTP2: false,
		TLSNextProto:      make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	t := &Tool{
		summarizer: summarizer,
		client:     &http.Client{Timeout: defaultHTTPTimeout, Transport: transport},
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// HTTP errors with a body are results, not errors.
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, resp.Status, string(body)), nil
	}

	content := extractReadableText(string(body))

	if t.summarizer == nil {
		return content, nil
	}
	answer, err := t.summarizer.Summarize(ctx, content, question)
	if err != nil {
		return "", fmt.Errorf("summarization failed: %w", err)
	}
	return answer, nil
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
