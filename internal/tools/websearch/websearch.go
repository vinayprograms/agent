// Package websearch provides an in-tree replacement for agentkit's built-in
// web_search tool.
//
// It exists because the pinned agentkit version's DuckDuckGo fallback hits the
// aggressively rate-limited https://duckduckgo.com/html/ endpoint with a
// bot-identifying User-Agent, which returns HTTP 403 the majority of the time.
// This implementation keeps agentkit's provider cascade
// (SearXNG > Brave > Tavily > DuckDuckGo) but routes the keyless DuckDuckGo
// path through the scraper-tolerant lite.duckduckgo.com endpoint with a
// browser User-Agent.
//
// The tool is registered over agentkit's built-in by calling
// registry.Register(websearch.New(...)) after tools.NewRegistry — Register
// overwrites by Name(), and this tool reports Name() == "web_search".
//
// Credentials are taken via the constructor rather than the registry's
// SetCredentials, because that setter type-asserts to agentkit's unexported
// *webSearchTool and therefore cannot reach a replacement tool.
//
// Note: The DuckDuckGo lite parser (parseDuckDuckGoLite) is unit-tested against
// representative HTML samples but has not been verified against live
// lite.duckduckgo.com output, which is IP-reputation-gated and may return
// HTTP 202 challenges from datacenter IPs. For production use, prefer SearXNG
// or a Brave/Tavily API key over the keyless DuckDuckGo fallback.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// CredProvider supplies API keys/URLs for search providers. *credentials.Credentials
// from agentkit satisfies this interface.
type CredProvider interface {
	GetAPIKey(provider string) string
}

// SearchResult is a single web search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Tool implements agentkit's tools.Tool interface for web_search.
type Tool struct {
	creds      CredProvider
	searxngURL string // resolved at construction (config value, else SEARXNG_URL env)
	provider   string // "auto" (cascade) or a pinned provider name
}

// New constructs the replacement web_search tool.
//
// searxngURL and provider come from config ([web].searxng_url / search_provider).
// Resolution order for the SearXNG URL is config-value-then-env, matching the
// rest of the codebase: if searxngURL is empty, SEARXNG_URL is consulted.
func New(creds CredProvider, searxngURL, provider string) *Tool {
	if searxngURL == "" {
		searxngURL = os.Getenv("SEARXNG_URL")
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "auto"
	}
	return &Tool{creds: creds, searxngURL: searxngURL, provider: provider}
}

func (t *Tool) Name() string { return "web_search" }

func (t *Tool) Description() string {
	return "Search the web. Returns titles, URLs, and short snippets. IMPORTANT: Snippets are brief previews only - use web_fetch on relevant URLs to get the full content needed for research. The standard flow is: web_search to discover sources, then web_fetch on 2-4 most relevant URLs."
}

func (t *Tool) Parameters() map[string]interface{} {
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

// httpClient is shared across searches with a defensive timeout; the caller's
// context (driven by the web_search timeout config) is the primary deadline.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// Global rate limiter to avoid hammering search backends.
var (
	searchMutex    sync.Mutex
	lastSearchTime time.Time
	searchCooldown = 500 * time.Millisecond
)

func (t *Tool) brave() string {
	if t.creds == nil {
		return os.Getenv("BRAVE_API_KEY")
	}
	if k := t.creds.GetAPIKey("brave"); k != "" {
		return k
	}
	return os.Getenv("BRAVE_API_KEY")
}

func (t *Tool) tavily() string {
	if t.creds == nil {
		return os.Getenv("TAVILY_API_KEY")
	}
	if k := t.creds.GetAPIKey("tavily"); k != "" {
		return k
	}
	return os.Getenv("TAVILY_API_KEY")
}

// Execute runs the search, honoring the configured provider selection.
func (t *Tool) Execute(ctx context.Context, rawArgs map[string]interface{}) (interface{}, error) {
	query, _ := rawArgs["query"].(string)
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("web_search: query is required")
	}

	count := 5
	if c, ok := toInt(rawArgs["count"]); ok {
		count = c
	}
	if count < 1 {
		count = 1
	} else if count > 10 {
		count = 10
	}

	// Rate limiting: serialize requests with a cooldown.
	searchMutex.Lock()
	elapsed := time.Since(lastSearchTime)
	if elapsed < searchCooldown {
		wait := searchCooldown - elapsed
		searchMutex.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		searchMutex.Lock()
	}
	lastSearchTime = time.Now()
	searchMutex.Unlock()

	braveKey, tavilyKey := t.brave(), t.tavily()

	switch t.provider {
	case "searxng":
		if t.searxngURL == "" {
			return nil, fmt.Errorf("web_search: search_provider=searxng but no searxng_url ([web].searxng_url or SEARXNG_URL) is set")
		}
		return searchSearXNG(ctx, query, count, t.searxngURL)
	case "brave":
		if braveKey == "" {
			return nil, fmt.Errorf("web_search: search_provider=brave but no Brave API key (credentials [brave] or BRAVE_API_KEY) is set")
		}
		return searchBrave(ctx, query, count, braveKey)
	case "tavily":
		if tavilyKey == "" {
			return nil, fmt.Errorf("web_search: search_provider=tavily but no Tavily API key (credentials [tavily] or TAVILY_API_KEY) is set")
		}
		return searchTavily(ctx, query, count, tavilyKey)
	case "duckduckgo":
		return searchDuckDuckGo(ctx, query, count)
	case "auto":
		// Cascade: SearXNG (self-hosted) > Brave > Tavily > DuckDuckGo.
		if t.searxngURL != "" {
			return searchSearXNG(ctx, query, count, t.searxngURL)
		}
		if braveKey != "" {
			return searchBrave(ctx, query, count, braveKey)
		}
		if tavilyKey != "" {
			return searchTavily(ctx, query, count, tavilyKey)
		}
		// No configured provider — DuckDuckGo is the keyless fallback, but it
		// is rate-limited and may fail. Warn the caller with actionable guidance
		// so failures aren't a silent mystery.
		results, err := searchDuckDuckGo(ctx, query, count)
		if err != nil {
			return nil, fmt.Errorf("web_search: %w — no search provider configured. Set [web].searxng_url, or provide a Brave/Tavily API key (credentials [brave]/[tavily] or BRAVE_API_KEY/TAVILY_API_KEY) for reliable results. DuckDuckGo is a best-effort fallback subject to rate limiting", err)
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("web_search: no results from DuckDuckGo fallback — no search provider configured. Set [web].searxng_url, or provide a Brave/Tavily API key for reliable results")
		}
		return results, nil
	default:
		return nil, fmt.Errorf("web_search: unknown search_provider %q (want auto|searxng|brave|tavily|duckduckgo)", t.provider)
	}
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// searchSearXNG queries a SearXNG instance's JSON API. The instance must have
// `format=json` enabled.
func searchSearXNG(ctx context.Context, query string, count int, baseURL string) ([]SearchResult, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	searchURL := fmt.Sprintf("%s/search?q=%s&format=json&categories=general",
		baseURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("searxng search error (%d): %s", resp.StatusCode, string(body))
	}

	var searxResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&searxResp); err != nil {
		return nil, fmt.Errorf("failed to parse searxng response: %w", err)
	}

	results := make([]SearchResult, 0, count)
	for i, r := range searxResp.Results {
		if i >= count {
			break
		}
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return results, nil
}

// searchBrave queries the Brave Search API.
func searchBrave(ctx context.Context, query string, count int, apiKey string) ([]SearchResult, error) {
	u := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), count)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("brave search error (%d): %s", resp.StatusCode, string(body))
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		return nil, fmt.Errorf("failed to parse brave response: %w", err)
	}

	results := make([]SearchResult, 0, len(braveResp.Web.Results))
	for _, r := range braveResp.Web.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Description})
	}
	return results, nil
}

// searchTavily queries the Tavily API.
func searchTavily(ctx context.Context, query string, count int, apiKey string) ([]SearchResult, error) {
	reqBody := map[string]interface{}{
		"api_key":     apiKey,
		"query":       query,
		"max_results": count,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily search error (%d): %s", resp.StatusCode, string(body))
	}

	var tavilyResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return nil, fmt.Errorf("failed to parse tavily response: %w", err)
	}

	results := make([]SearchResult, 0, len(tavilyResp.Results))
	for _, r := range tavilyResp.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return results, nil
}
