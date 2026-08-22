package websearch

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// browserUserAgent is a realistic desktop browser UA. The previous bot-identifying
// User-Agent ("HeadlessAgent/1.0 (+github...)") combined with the duckduckgo.com/html
// endpoint was reliably 403'd; a browser UA against the lite endpoint is tolerated.
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// DuckDuckGo lite endpoint. lite.duckduckgo.com serves a minimal HTML page that
// is far more scraper-tolerant than the /html/ endpoint.
const ddgLiteURL = "https://lite.duckduckgo.com/lite/"

// searchDuckDuckGo searches via DuckDuckGo's lite endpoint (no API key
// needed), retrying rate-limit responses with capped exponential backoff.
func (t *Tool) searchDuckDuckGo(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if err := t.ddgLimit.wait(ctx, t.now); err != nil {
		return nil, err
	}

	backoff := t.ddgBackoff
	var lastErr error

	for attempt := 0; attempt <= t.ddgMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, t.ddgMaxBackoff)
		}

		// The lite endpoint takes the query as POST form data.
		form := url.Values{}
		form.Set("q", query)
		req, err := http.NewRequestWithContext(ctx, "POST", t.ddgURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", browserUserAgent)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Referer", t.ddgURL)

		resp, err := t.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("duckduckgo search failed: %w", err)
			continue
		}

		if resp.StatusCode == 202 || resp.StatusCode == 403 || resp.StatusCode == 429 {
			resp.Body.Close()
			lastErr = fmt.Errorf("duckduckgo rate limited (status %d), retrying", resp.StatusCode)
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("duckduckgo search error: status %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read duckduckgo response: %w", err)
		}
		return parseDuckDuckGoLite(string(body), count), nil
	}

	return nil, fmt.Errorf("duckduckgo search failed after %d retries: %w", t.ddgMaxRetries, lastErr)
}

// Lite result anchors look like:
//
//	<a rel="nofollow" href="//duckduckgo.com/l/?uddg=https%3A%2F%2F...&rut=..." class="result-link">Title</a>
//
// and snippets are in a following cell:
//
//	<td class="result-snippet">Snippet text</td>
//
// Attribute order/quoting can vary, so the regexes tolerate single/double quotes
// and only anchor on the distinguishing class names.
var (
	ddgLinkRe    = regexp.MustCompile(`(?is)<a\b[^>]*\bhref=["']([^"']+)["'][^>]*\bclass=["'][^"']*\bresult-link\b[^"']*["'][^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?is)<td[^>]*\bclass=["'][^"']*\bresult-snippet\b[^"']*["'][^>]*>(.*?)</td>`)
	tagRe        = regexp.MustCompile(`<[^>]*>`)
)

// parseDuckDuckGoLite extracts results from the lite endpoint HTML.
func parseDuckDuckGoLite(body string, count int) []SearchResult {
	links := ddgLinkRe.FindAllStringSubmatch(body, count*2)
	snippets := ddgSnippetRe.FindAllStringSubmatch(body, count*2)

	results := make([]SearchResult, 0, count)
	for i := 0; i < len(links) && len(results) < count; i++ {
		rawURL := unwrapDDGRedirect(links[i][1])
		if !strings.HasPrefix(rawURL, "http") {
			continue
		}
		title := html.UnescapeString(stripTags(links[i][2]))

		snippet := ""
		if i < len(snippets) {
			snippet = html.UnescapeString(stripTags(snippets[i][1]))
		}

		results = append(results, SearchResult{
			Title:   strings.TrimSpace(title),
			URL:     rawURL,
			Snippet: strings.TrimSpace(snippet),
		})
	}
	return results
}

// unwrapDDGRedirect resolves DuckDuckGo's /l/?uddg= redirect wrapper to the
// underlying target URL. Returns the input unchanged if it is not wrapped.
func unwrapDDGRedirect(href string) string {
	href = strings.TrimSpace(href)
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if target := u.Query().Get("uddg"); target != "" {
		return target // url.Parse already decoded the query value
	}
	return href
}

func stripTags(s string) string {
	return strings.TrimSpace(tagRe.ReplaceAllString(s, ""))
}
