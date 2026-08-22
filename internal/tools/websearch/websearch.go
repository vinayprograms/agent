// Package websearch provides an in-tree replacement for agentkit's built-in
// web_search tool.
//
// It exists because agentkit's DuckDuckGo fallback hits the aggressively
// rate-limited https://duckduckgo.com/html/ endpoint with a bot-identifying
// User-Agent, which returns HTTP 403 the majority of the time. This
// implementation keeps agentkit's provider cascade
// (SearXNG > Brave > Tavily > DuckDuckGo) but routes the keyless DuckDuckGo
// path through the scraper-tolerant lite.duckduckgo.com endpoint with a
// browser User-Agent. It also lets config pin a single provider.
//
// Registration: agentkit v1.2.0's Registry.Register returns an error on a
// duplicate name, so this tool must be registered INSTEAD of tools.Search,
// never over it:
//
//	reg.Register(tools.New(websearch.New(creds, cfg.SearxngURL, cfg.SearchProvider)))
//
// The tool reports Name() == "web_search" and accepts the same arguments
// ("query", "count") and returns the same text format as the built-in, so
// prompts written against the built-in keep working.
//
// Credentials are resolved once, in New, the way tools.Search resolves them:
// an explicit config value (searxngURL) wins, then the credentials.Lookup
// entry for the provider ("searxng", "brave", "tavily"), then the provider's
// environment variable (SEARXNG_URL, BRAVE_API_KEY, TAVILY_API_KEY).
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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/tools"
)

// SearchResult is a single web search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// ErrNoProvider is returned (wrapped) when no search provider is configured
// and the keyless DuckDuckGo fallback produced nothing usable.
var ErrNoProvider = errors.New("no search provider configured")

// Provider endpoints. Fields on Tool so tests can point them at httptest servers.
const (
	braveSearchURL  = "https://api.search.brave.com/res/v1/web/search"
	tavilySearchURL = "https://api.tavily.com/search"
)

// Defaults for the rate limiting described on Tool.
const (
	defaultHTTPTimeout   = 30 * time.Second
	defaultCooldown      = 500 * time.Millisecond
	defaultDDGCooldown   = 2 * time.Second
	defaultDDGBackoff    = 2 * time.Second
	defaultDDGMaxBackoff = 5 * time.Second
	defaultDDGMaxRetries = 3
)

// Tool implements agentkit's tools.Tool interface for web_search.
//
// Every search waits out a short cooldown since the previous one on the same
// Tool; DuckDuckGo adds a longer cooldown plus bounded retries with backoff
// on 202/403/429 (worst case 2s + 3 retries of up to 5s stays inside the
// default 30s web_search timeout). Tools do not share limiter state.
type Tool struct {
	searxngURL string // config value > credentials "searxng" > SEARXNG_URL env
	braveKey   string // credentials "brave" > BRAVE_API_KEY env
	tavilyKey  string // credentials "tavily" > TAVILY_API_KEY env
	provider   string // "auto" (cascade) or a pinned provider name

	// client has a defensive timeout; the caller's context (driven by the
	// web_search timeout config) is the primary deadline.
	client    *http.Client
	braveURL  string
	tavilyURL string
	ddgURL    string

	now           func() time.Time
	searchLimit   limiter // all providers
	ddgLimit      limiter // DuckDuckGo only
	ddgBackoff    time.Duration
	ddgMaxBackoff time.Duration
	ddgMaxRetries int
}

var _ tools.Tool = (*Tool)(nil)

// Option configures a Tool.
type Option func(*Tool)

// WithHTTPTimeout sets the HTTP client's defensive timeout (default 30s).
// The caller's context remains the primary deadline.
func WithHTTPTimeout(d time.Duration) Option {
	return func(t *Tool) { t.client.Timeout = d }
}

// New constructs the replacement web_search tool.
//
// creds may be nil. searxngURL and provider come from config
// ([web].searxng_url / search_provider); see the package doc for how
// credentials are resolved.
func New(creds credentials.Lookup, searxngURL, provider string, opts ...Option) *Tool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "auto"
	}
	if searxngURL == "" {
		searxngURL = resolve(creds, "searxng", "SEARXNG_URL")
	}
	t := &Tool{
		searxngURL:    searxngURL,
		braveKey:      resolve(creds, "brave", "BRAVE_API_KEY"),
		tavilyKey:     resolve(creds, "tavily", "TAVILY_API_KEY"),
		provider:      provider,
		client:        &http.Client{Timeout: defaultHTTPTimeout},
		braveURL:      braveSearchURL,
		tavilyURL:     tavilySearchURL,
		ddgURL:        ddgLiteURL,
		now:           time.Now,
		searchLimit:   limiter{cooldown: defaultCooldown},
		ddgLimit:      limiter{cooldown: defaultDDGCooldown},
		ddgBackoff:    defaultDDGBackoff,
		ddgMaxBackoff: defaultDDGMaxBackoff,
		ddgMaxRetries: defaultDDGMaxRetries,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// limiter enforces a minimum gap between consecutive calls.
type limiter struct {
	mu       sync.Mutex
	cooldown time.Duration
	last     time.Time
}

// wait blocks until cooldown has passed since the previous call (or ctx
// ends), then claims the slot.
func (l *limiter) wait(ctx context.Context, now func() time.Time) error {
	l.mu.Lock()
	if remaining := l.cooldown - now().Sub(l.last); remaining > 0 {
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(remaining):
		}
		l.mu.Lock()
	}
	l.last = now()
	l.mu.Unlock()
	return nil
}

// resolve returns the credential for provider, falling back to envVar.
func resolve(creds credentials.Lookup, provider, envVar string) string {
	if creds != nil {
		if v := string(creds.Get(provider)); v != "" {
			return v
		}
	}
	return os.Getenv(envVar)
}

func (t *Tool) Name() string { return "web_search" }

func (t *Tool) Description() string {
	return "Search the web. Returns titles, URLs, and short snippets. " +
		"IMPORTANT: Snippets are brief previews only — use web_fetch on relevant URLs " +
		"to get the full content needed for research. The standard flow is: web_search " +
		"to discover sources, then web_fetch on 2-4 most relevant URLs."
}

func (t *Tool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"query": {
			Type:        tools.StringParam,
			Description: "Search query",
			Required:    true,
		},
		"count": {
			Type:        tools.IntParam,
			Description: "Number of results (1-10, default 5)",
		},
	}
}

// Execute runs the search, honoring the configured provider selection.
func (t *Tool) Execute(ctx context.Context, args tools.Args) (string, error) {
	query, err := args.String("query")
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("web_search: query is required")
	}
	count := min(max(args.IntOr("count", 5), 1), 10)

	if err := t.searchLimit.wait(ctx, t.now); err != nil {
		return "", err
	}
	results, err := t.search(ctx, query, count)
	if err != nil {
		return "", err
	}
	return formatResults(results), nil
}

func (t *Tool) search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	switch t.provider {
	case "searxng":
		if t.searxngURL == "" {
			return nil, fmt.Errorf("web_search: search_provider=searxng but no searxng_url ([web].searxng_url, credentials [searxng] or SEARXNG_URL) is set")
		}
		return t.searchSearXNG(ctx, query, count)
	case "brave":
		if t.braveKey == "" {
			return nil, fmt.Errorf("web_search: search_provider=brave but no Brave API key (credentials [brave] or BRAVE_API_KEY) is set")
		}
		return t.searchBrave(ctx, query, count)
	case "tavily":
		if t.tavilyKey == "" {
			return nil, fmt.Errorf("web_search: search_provider=tavily but no Tavily API key (credentials [tavily] or TAVILY_API_KEY) is set")
		}
		return t.searchTavily(ctx, query, count)
	case "duckduckgo":
		return t.searchDuckDuckGo(ctx, query, count)
	case "auto":
		// Cascade: SearXNG (self-hosted) > Brave > Tavily > DuckDuckGo.
		if t.searxngURL != "" {
			return t.searchSearXNG(ctx, query, count)
		}
		if t.braveKey != "" {
			return t.searchBrave(ctx, query, count)
		}
		if t.tavilyKey != "" {
			return t.searchTavily(ctx, query, count)
		}
		// No configured provider — DuckDuckGo is the keyless fallback, but it
		// is rate-limited and may fail. Warn the caller with actionable guidance
		// so failures aren't a silent mystery.
		results, err := t.searchDuckDuckGo(ctx, query, count)
		if err != nil {
			return nil, fmt.Errorf("web_search: %w (DuckDuckGo fallback: %w). %s", ErrNoProvider, err, noProviderHint)
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("web_search: %w (no results from DuckDuckGo fallback). %s", ErrNoProvider, noProviderHint)
		}
		return results, nil
	default:
		return nil, fmt.Errorf("web_search: unknown search_provider %q (want auto|searxng|brave|tavily|duckduckgo)", t.provider)
	}
}

// noProviderHint tells the caller how to get off the best-effort fallback.
const noProviderHint = "Set [web].searxng_url, or provide a Brave/Tavily API key " +
	"(credentials [brave]/[tavily] or BRAVE_API_KEY/TAVILY_API_KEY) for reliable results; " +
	"DuckDuckGo is a best-effort fallback subject to rate limiting"

// formatResults renders results in the same text layout as agentkit's built-in
// web_search, so prompts tuned against the built-in keep working.
func formatResults(results []SearchResult) string {
	if len(results) == 0 {
		return "No results found."
	}
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%d. %s\n   %s", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "\n   %s", r.Snippet)
		}
	}
	return b.String()
}

// searchSearXNG queries a SearXNG instance's JSON API. The instance must have
// `format=json` enabled.
func (t *Tool) searchSearXNG(ctx context.Context, query string, count int) ([]SearchResult, error) {
	searchURL := fmt.Sprintf("%s/search?q=%s&format=json&categories=general",
		strings.TrimSuffix(t.searxngURL, "/"), url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUserAgent)

	resp, err := t.client.Do(req)
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
func (t *Tool) searchBrave(ctx context.Context, query string, count int) ([]SearchResult, error) {
	u := fmt.Sprintf("%s?q=%s&count=%d", t.braveURL, url.QueryEscape(query), count)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", t.braveKey)
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
func (t *Tool) searchTavily(ctx context.Context, query string, count int) ([]SearchResult, error) {
	reqBody := map[string]any{
		"api_key":     t.tavilyKey,
		"query":       query,
		"max_results": count,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", t.tavilyURL, bytes.NewReader(bodyBytes))
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
