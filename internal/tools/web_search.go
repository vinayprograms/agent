package tools

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vinayprograms/agentkit/policy"
)

// CredentialProvider supplies API keys for search backends.
type CredentialProvider interface {
	GetAPIKey(provider string) string
}

// WebSearchTool replaces agentkit's built-in web_search with parity on all
// backends (Brave → Tavily → DuckDuckGo) plus:
//   - configurable inter-query cooldown (DDG only — avoids 202 rate limiting)
//   - in-session result cache shared across sub-agents
//   - exponential backoff with jitter on DDG rate-limit responses
//   - HTTP/1.1-only transport (same reasoning as WebFetchTool)
type WebSearchTool struct {
	policy      *policy.Policy
	credentials CredentialProvider
	cooldown    time.Duration
	client      *http.Client
	ddgMu       sync.Mutex
	lastDDQ     time.Time
	cacheMu     sync.RWMutex
	cache       map[string]cachedResult
	cacheTTL    time.Duration
}

type cachedResult struct {
	results []SearchResult
	expiry  time.Time
}

// SearchResult is returned by all backends.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func NewWebSearch(pol *policy.Policy, cooldownMS int, creds CredentialProvider) *WebSearchTool {
	if cooldownMS <= 0 {
		cooldownMS = 2000
	}
	transport := &http.Transport{
		TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	return &WebSearchTool{
		policy:      pol,
		credentials: creds,
		cooldown:    time.Duration(cooldownMS) * time.Millisecond,
		client:      &http.Client{Transport: transport},
		cache:       make(map[string]cachedResult),
		cacheTTL:    5 * time.Minute,
	}
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string {
	return "Search the web. Returns titles, URLs, and short snippets. IMPORTANT: Snippets are brief previews only - use web_fetch on relevant URLs to get the full content needed for research. The standard flow is: web_search to discover sources, then web_fetch on 2-4 most relevant URLs."
}

func (t *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results (1-10, default 5)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required")
	}
	count := 5
	if c, ok := args["count"].(float64); ok {
		count = int(c)
		if count < 1 {
			count = 1
		} else if count > 10 {
			count = 10
		}
	}

	cacheKey := fmt.Sprintf("%s:%d", query, count)
	t.cacheMu.RLock()
	if entry, hit := t.cache[cacheKey]; hit && time.Now().Before(entry.expiry) {
		t.cacheMu.RUnlock()
		return entry.results, nil
	}
	t.cacheMu.RUnlock()

	// Resolve API keys: credentials file takes priority, env vars as fallback.
	var braveKey, tavilyKey string
	if t.credentials != nil {
		braveKey = t.credentials.GetAPIKey("brave")
		tavilyKey = t.credentials.GetAPIKey("tavily")
	}
	if braveKey == "" {
		braveKey = os.Getenv("BRAVE_API_KEY")
	}
	if tavilyKey == "" {
		tavilyKey = os.Getenv("TAVILY_API_KEY")
	}

	var (
		results []SearchResult
		err     error
	)
	switch {
	case braveKey != "":
		results, err = t.searchBrave(ctx, query, count, braveKey)
	case tavilyKey != "":
		results, err = t.searchTavily(ctx, query, count, tavilyKey)
	default:
		results, err = t.searchDDG(ctx, query, count)
	}
	if err != nil {
		return nil, err
	}

	t.cacheMu.Lock()
	t.cache[cacheKey] = cachedResult{results: results, expiry: time.Now().Add(t.cacheTTL)}
	t.cacheMu.Unlock()

	return results, nil
}

// --- Brave ---

func (t *WebSearchTool) searchBrave(ctx context.Context, query string, count int, apiKey string) ([]SearchResult, error) {
	url := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		strings.ReplaceAll(query, " ", "+"), count)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("brave search error (%d): %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("failed to parse brave response: %w", err)
	}

	results := make([]SearchResult, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Description})
	}
	return results, nil
}

// --- Tavily ---

func (t *WebSearchTool) searchTavily(ctx context.Context, query string, count int, apiKey string) ([]SearchResult, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"api_key":     apiKey,
		"query":       query,
		"max_results": count,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily search error (%d): %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("failed to parse tavily response: %w", err)
	}

	results := make([]SearchResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return results, nil
}

// --- DuckDuckGo (with cooldown + retry) ---

func (t *WebSearchTool) searchDDG(ctx context.Context, query string, count int) ([]SearchResult, error) {
	// Serialize DDG queries and enforce cooldown to avoid 202 rate limiting.
	t.ddgMu.Lock()
	wait := t.cooldown - time.Since(t.lastDDQ)
	if wait > 0 {
		t.ddgMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		t.ddgMu.Lock()
	}
	t.lastDDQ = time.Now()
	t.ddgMu.Unlock()

	return t.ddgWithRetry(ctx, query, count)
}

const maxDDGRetries = 3

func (t *WebSearchTool) ddgWithRetry(ctx context.Context, query string, count int) ([]SearchResult, error) {
	var lastErr error
	for attempt := range maxDDGRetries {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s ± 20% jitter.
			base := time.Duration(1<<uint(attempt-1)) * time.Second
			jitter := time.Duration(rand.Int63n(int64(base / 5)))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(base + jitter):
			}
		}

		results, rateLimited, err := t.queryDDG(ctx, query, count)
		if err == nil {
			return results, nil
		}
		lastErr = err
		if !rateLimited {
			return nil, err
		}
	}
	return nil, fmt.Errorf("duckduckgo search failed after %d retries: %w", maxDDGRetries, lastErr)
}

func (t *WebSearchTool) queryDDG(ctx context.Context, query string, count int) ([]SearchResult, bool, error) {
	url := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s",
		strings.ReplaceAll(strings.ReplaceAll(query, " ", "+"), "&", "%26"))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "Lynx/2.8.9rel.1 libwww-FM/2.14")
	req.Header.Set("Accept", "text/html")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("duckduckgo search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 202 || resp.StatusCode == 429 {
		return nil, true, fmt.Errorf("duckduckgo rate limited (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, false, fmt.Errorf("duckduckgo search error: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, false, fmt.Errorf("failed to read duckduckgo response: %w", err)
	}

	return parseDDGHTML(string(body), count), false, nil
}

var (
	ddgLinkRe    = regexp.MustCompile(`<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>([^<]+)</a>`)
	ddgSnippetRe = regexp.MustCompile(`<a[^>]+class="result__snippet"[^>]*>([^<]+(?:<[^>]+>[^<]*</[^>]+>)*[^<]*)</a>`)
	ddgTagRe     = regexp.MustCompile(`<[^>]*>`)
)

func parseDDGHTML(html string, count int) []SearchResult {
	links := ddgLinkRe.FindAllStringSubmatch(html, count*2)
	snippets := ddgSnippetRe.FindAllStringSubmatch(html, count*2)

	var results []SearchResult
	for i, link := range links {
		if len(results) >= count {
			break
		}
		u := link[1]
		title := strings.TrimSpace(link[2])

		if strings.Contains(u, "uddg=") {
			if parts := strings.Split(u, "uddg="); len(parts) > 1 {
				decoded := decodeURLEncoding(parts[1])
				if idx := strings.Index(decoded, "&"); idx != -1 {
					decoded = decoded[:idx]
				}
				u = decoded
			}
		}
		if !strings.HasPrefix(u, "http") {
			continue
		}

		snippet := ""
		if i < len(snippets) {
			snippet = strings.TrimSpace(ddgTagRe.ReplaceAllString(snippets[i][1], ""))
			snippet = decodeHTMLEntities(snippet)
		}

		results = append(results, SearchResult{
			Title:   decodeHTMLEntities(title),
			URL:     u,
			Snippet: snippet,
		})
	}
	return results
}

func decodeURLEncoding(s string) string {
	return strings.NewReplacer(
		"%3A", ":",
		"%2F", "/",
		"%3F", "?",
		"%3D", "=",
		"%26", "&",
		"%25", "%",
	).Replace(s)
}

func decodeHTMLEntities(s string) string {
	return strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&nbsp;", " ",
	).Replace(s)
}
