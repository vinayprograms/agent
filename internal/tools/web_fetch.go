package tools

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/vinayprograms/agentkit/policy"
)

// Summarizer extracts an answer from fetched content.
type Summarizer interface {
	Summarize(ctx context.Context, content, question string) (string, error)
}

// WebFetchTool replaces agentkit's built-in web_fetch with an HTTP/1.1-only
// client to avoid HTTP/2 fingerprint rejection by enterprise CDNs (Akamai,
// Cloudflare). Browser-like headers reduce bot detection false positives.
type WebFetchTool struct {
	policy     *policy.Policy
	summarizer Summarizer
	client     *http.Client
}

func NewWebFetch(pol *policy.Policy, summarizer Summarizer) *WebFetchTool {
	// Disable HTTP/2 ALPN negotiation — Go's h2 SETTINGS fingerprint is
	// trivially identifiable and causes INTERNAL_ERROR from CDN WAFs.
	transport := &http.Transport{
		TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	return &WebFetchTool{
		policy:     pol,
		summarizer: summarizer,
		client:     &http.Client{Transport: transport},
	}
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Description() string {
	return "Fetch and summarize content from a URL. Requires a question/prompt - the tool returns a concise answer based on the page content, not the raw page. Use after web_search to get specific information from promising results."
}

func (t *WebFetchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "URL to fetch (typically from web_search results)",
			},
			"question": map[string]interface{}{
				"type":        "string",
				"description": "What information to extract from the page",
			},
		},
		"required": []string{"url", "question"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	url, _ := args["url"].(string)
	question, _ := args["question"].(string)
	if url == "" {
		return nil, fmt.Errorf("url is required")
	}
	if question == "" {
		return nil, fmt.Errorf("question is required")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}

	// Mimic a real browser request to pass WAF/CDN checks.
	// Accept-Encoding is intentionally omitted — Go decompresses gzip
	// transparently only when it adds the header itself.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("DNT", "1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch failed: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	content := extractText(string(body))

	if t.summarizer != nil {
		return t.summarizer.Summarize(ctx, content, question)
	}

	if len(content) > 15000 {
		content = content[:15000] + "\n\n[Content truncated]"
	}
	return content, nil
}

var (
	reScript   = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle    = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reHead     = regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
	reNav      = regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`)
	reFooter   = regexp.MustCompile(`(?is)<footer[^>]*>.*?</footer>`)
	reComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBlock    = regexp.MustCompile(`(?i)<(p|div|br|h[1-6]|li|tr|article|section)[^>]*>`)
	reTags     = regexp.MustCompile(`<[^>]+>`)
	reSpaces   = regexp.MustCompile(`[ \t]+`)
	reNewlines = regexp.MustCompile(`\n{3,}`)
)

func extractText(html string) string {
	html = reScript.ReplaceAllString(html, "")
	html = reStyle.ReplaceAllString(html, "")
	html = reHead.ReplaceAllString(html, "")
	html = reNav.ReplaceAllString(html, "")
	html = reFooter.ReplaceAllString(html, "")
	html = reComment.ReplaceAllString(html, "")
	html = reBlock.ReplaceAllString(html, "\n")
	text := reTags.ReplaceAllString(html, "")

	text = strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&mdash;", "—",
		"&ndash;", "–",
		"&ldquo;", "“",
		"&rdquo;", "”",
	).Replace(text)

	text = reSpaces.ReplaceAllString(text, " ")
	text = reNewlines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
