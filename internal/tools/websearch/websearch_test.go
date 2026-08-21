package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/tools"
)

// Make the rate limiters and retry backoffs instant so tests don't sleep.
func init() {
	searchCooldown = 0
	ddgCooldown = 0
	ddgBackoff = 0
	ddgMaxBackoff = 0
}

// fakeCreds is a credentials.Lookup backed by a map.
type fakeCreds map[string]string

func (f fakeCreds) Get(p string) credentials.Credential { return credentials.Credential(f[p]) }
func (f fakeCreds) Providers() []string                 { return nil }

func args(t *testing.T, raw map[string]any) tools.Args {
	t.Helper()
	a, err := tools.Validate((*Tool)(nil).Parameters(), raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return a
}

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTool builds a tool whose provider endpoints all point at srv.
func newTool(srv *httptest.Server, creds credentials.Lookup, searxngURL, provider string) *Tool {
	tl := New(creds, searxngURL, provider)
	tl.braveURL, tl.tavilyURL, tl.ddgURL = srv.URL, srv.URL, srv.URL
	return tl
}

const (
	searxJSON  = `{"results":[{"title":"A","url":"https://a.example","content":"sa"},{"title":"B","url":"https://b.example","content":"sb"}]}`
	braveJSON  = `{"web":{"results":[{"title":"A","url":"https://a.example","description":"sa"}]}}`
	tavilyJSON = `{"results":[{"title":"A","url":"https://a.example","content":""}]}`
)

func TestNew_Resolution(t *testing.T) {
	t.Setenv("SEARXNG_URL", "http://env-searx")
	t.Setenv("BRAVE_API_KEY", "env-brave")
	t.Setenv("TAVILY_API_KEY", "env-tavily")

	tests := []struct {
		name                string
		creds               credentials.Lookup
		searxng, provider   string
		wantSearx, wantBrav string
		wantTav, wantProv   string
	}{
		{"nil creds falls back to env", nil, "", "", "http://env-searx", "env-brave", "env-tavily", "auto"},
		{"config searxng wins", fakeCreds{"searxng": "http://cred"}, "http://cfg", " Brave ", "http://cfg", "env-brave", "env-tavily", "brave"},
		{"creds win over env", fakeCreds{"searxng": "http://cred", "brave": "cb", "tavily": "ct"}, "", "tavily", "http://cred", "cb", "ct", "tavily"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tl := New(tc.creds, tc.searxng, tc.provider)
			got := []string{tl.searxngURL, tl.braveKey, tl.tavilyKey, tl.provider}
			want := []string{tc.wantSearx, tc.wantBrav, tc.wantTav, tc.wantProv}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("field %d = %q, want %q", i, got[i], want[i])
				}
			}
		})
	}
}

func TestToolMetadata(t *testing.T) {
	tl := New(nil, "", "")
	if tl.Name() != "web_search" {
		t.Errorf("Name = %q", tl.Name())
	}
	if !strings.Contains(tl.Description(), "web_fetch") {
		t.Errorf("Description should steer to web_fetch: %q", tl.Description())
	}
	p := tl.Parameters()
	if !p["query"].Required || p["query"].Type != tools.StringParam {
		t.Errorf("query param = %+v", p["query"])
	}
	if p["count"].Type != tools.IntParam || p["count"].Required {
		t.Errorf("count param = %+v", p["count"])
	}
}

func TestExecute_Providers(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		creds    fakeCreds
		searxng  bool // point searxngURL at the server
		status   int
		body     string
		want     string // substring of output
		wantErr  string // substring of error
	}{
		{name: "searxng pinned", provider: "searxng", searxng: true, status: 200, body: searxJSON, want: "1. A\n   https://a.example\n   sa\n\n2. B"},
		{name: "searxng missing url", provider: "searxng", wantErr: "no searxng_url"},
		{name: "searxng bad status", provider: "searxng", searxng: true, status: 500, body: "boom", wantErr: "searxng search error (500): boom"},
		{name: "searxng bad json", provider: "searxng", searxng: true, status: 200, body: "{", wantErr: "parse searxng"},
		{name: "brave pinned", provider: "brave", creds: fakeCreds{"brave": "k"}, status: 200, body: braveJSON, want: "1. A\n   https://a.example\n   sa"},
		{name: "brave missing key", provider: "brave", wantErr: "no Brave API key"},
		{name: "brave bad status", provider: "brave", creds: fakeCreds{"brave": "k"}, status: 401, body: "nope", wantErr: "brave search error (401): nope"},
		{name: "brave bad json", provider: "brave", creds: fakeCreds{"brave": "k"}, status: 200, body: "{", wantErr: "parse brave"},
		{name: "tavily pinned, empty snippet omitted", provider: "tavily", creds: fakeCreds{"tavily": "k"}, status: 200, body: tavilyJSON, want: "1. A\n   https://a.example"},
		{name: "tavily missing key", provider: "tavily", wantErr: "no Tavily API key"},
		{name: "tavily bad status", provider: "tavily", creds: fakeCreds{"tavily": "k"}, status: 403, body: "no", wantErr: "tavily search error (403): no"},
		{name: "tavily bad json", provider: "tavily", creds: fakeCreds{"tavily": "k"}, status: 200, body: "{", wantErr: "parse tavily"},
		{name: "duckduckgo pinned", provider: "duckduckgo", status: 200, body: liteSample, want: "1. The Go Programming Language & Docs\n   https://go.dev/doc/"},
		{name: "duckduckgo hard error", provider: "duckduckgo", status: 500, wantErr: "duckduckgo search error: status 500"},
		{name: "duckduckgo rate limited exhausts retries", provider: "duckduckgo", status: 429, wantErr: "failed after 3 retries"},
		{name: "auto > searxng", provider: "auto", searxng: true, creds: fakeCreds{"brave": "k"}, status: 200, body: searxJSON, want: "2. B"},
		{name: "auto > brave", provider: "", creds: fakeCreds{"brave": "k", "tavily": "k"}, status: 200, body: braveJSON, want: "1. A"},
		{name: "auto > tavily", provider: "auto", creds: fakeCreds{"tavily": "k"}, status: 200, body: tavilyJSON, want: "1. A"},
		{name: "auto > duckduckgo", provider: "auto", status: 200, body: liteSample, want: "pkg.go.dev"},
		{name: "auto duckduckgo error is actionable", provider: "auto", status: 500, wantErr: "searxng_url, or provide a Brave/Tavily API key (credentials [brave]/[tavily] or BRAVE_API_KEY/TAVILY_API_KEY)"},
		{name: "auto duckduckgo no results", provider: "auto", status: 200, body: "<html></html>", wantErr: "no results from DuckDuckGo fallback"},
		{name: "unknown provider", provider: "bing", wantErr: `unknown search_provider "bing"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Env must not leak provider keys into "missing key" cases.
			t.Setenv("SEARXNG_URL", "")
			t.Setenv("BRAVE_API_KEY", "")
			t.Setenv("TAVILY_API_KEY", "")

			srv := serve(t, tc.status, tc.body)
			searxng := ""
			if tc.searxng {
				searxng = srv.URL + "/"
			}
			tl := newTool(srv, tc.creds, searxng, tc.provider)

			got, err := tl.Execute(context.Background(), args(t, map[string]any{"query": "go docs", "count": 2}))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("output = %q, want containing %q", got, tc.want)
			}
		})
	}
}

func TestExecute_QueryValidation(t *testing.T) {
	tl := New(nil, "", "")
	for name, raw := range map[string]map[string]any{
		"missing": {},
		"blank":   {"query": "   "},
	} {
		t.Run(name, func(t *testing.T) {
			// Validate would reject a missing required arg before Execute; build
			// Args without the required flag to exercise Execute's own checks.
			a, err := tools.Validate(nil, raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tl.Execute(context.Background(), a); err == nil || !strings.Contains(err.Error(), "query is required") {
				t.Errorf("err = %v, want query is required", err)
			}
		})
	}
}

func TestExecute_CountClamped(t *testing.T) {
	var gotCount atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCount.Store(r.URL.Query().Get("count"))
		_, _ = w.Write([]byte(braveJSON))
	}))
	t.Cleanup(srv.Close)

	for in, want := range map[int]string{0: "1", 99: "10", 3: "3"} {
		tl := newTool(srv, fakeCreds{"brave": "k"}, "", "brave")
		if _, err := tl.Execute(context.Background(), args(t, map[string]any{"query": "q", "count": in})); err != nil {
			t.Fatal(err)
		}
		if got := gotCount.Load(); got != want {
			t.Errorf("count %d sent as %v, want %s", in, got, want)
		}
	}
}

func TestExecute_SearxngTruncatesToCount(t *testing.T) {
	srv := serve(t, 200, searxJSON)
	tl := newTool(srv, nil, srv.URL, "searxng")
	got, err := tl.Execute(context.Background(), args(t, map[string]any{"query": "q", "count": 1}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "2. B") {
		t.Errorf("expected only one result, got %q", got)
	}
}

func TestExecute_NoResultsMessage(t *testing.T) {
	srv := serve(t, 200, `{"results":[]}`)
	tl := newTool(srv, nil, srv.URL, "searxng")
	got, err := tl.Execute(context.Background(), args(t, map[string]any{"query": "q"}))
	if err != nil {
		t.Fatal(err)
	}
	if got != "No results found." {
		t.Errorf("got %q", got)
	}
}

func TestExecute_TransportErrors(t *testing.T) {
	// A closed server makes every provider's client.Do fail.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	for _, tc := range []struct {
		provider string
		creds    fakeCreds
		wantErr  string
	}{
		{"searxng", nil, "searxng search failed"},
		{"brave", fakeCreds{"brave": "k"}, "brave search failed"},
		{"tavily", fakeCreds{"tavily": "k"}, "tavily search failed"},
		{"duckduckgo", nil, "duckduckgo search failed"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			tl := newTool(srv, tc.creds, srv.URL, tc.provider)
			_, err := tl.Execute(context.Background(), args(t, map[string]any{"query": "q"}))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestExecute_BadEndpointURL(t *testing.T) {
	// A malformed endpoint makes http.NewRequestWithContext itself fail.
	srv := serve(t, 200, "")
	for _, provider := range []string{"searxng", "brave", "tavily", "duckduckgo"} {
		t.Run(provider, func(t *testing.T) {
			tl := newTool(srv, fakeCreds{"brave": "k", "tavily": "k"}, "\x7f", provider)
			tl.braveURL, tl.tavilyURL, tl.ddgURL = "\x7f", "\x7f", "\x7f"
			if _, err := tl.Execute(context.Background(), args(t, map[string]any{"query": "q"})); err == nil {
				t.Error("expected request construction error")
			}
		})
	}
}

func TestExecute_ReadBodyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)
	tl := newTool(srv, nil, "", "duckduckgo")
	_, err := tl.Execute(context.Background(), args(t, map[string]any{"query": "q"}))
	if err == nil || !strings.Contains(err.Error(), "read duckduckgo response") {
		t.Errorf("err = %v", err)
	}
}

func TestExecute_ContextCancelled(t *testing.T) {
	t.Run("search cooldown", func(t *testing.T) {
		searchCooldown = time.Hour
		lastSearchTime = time.Now()
		t.Cleanup(func() { searchCooldown = 0 })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := New(nil, "", "").Execute(ctx, args(t, map[string]any{"query": "q"}))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("ddg cooldown", func(t *testing.T) {
		ddgCooldown = time.Hour
		ddgLastSearch = time.Now()
		t.Cleanup(func() { ddgCooldown = 0 })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := New(nil, "", "duckduckgo").Execute(ctx, args(t, map[string]any{"query": "q"}))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("ddg retry backoff", func(t *testing.T) {
		srv := serve(t, 429, "")
		ddgBackoff = time.Hour
		t.Cleanup(func() { ddgBackoff = 0 })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		// First attempt sends (ctx already cancelled → transport error), then
		// the backoff select observes ctx.Done.
		_, err := newTool(srv, nil, "", "duckduckgo").Execute(ctx, args(t, map[string]any{"query": "q"}))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestExecute_CooldownWaits(t *testing.T) {
	srv := serve(t, 200, liteSample)
	searchCooldown, ddgCooldown = 5*time.Millisecond, 30*time.Millisecond
	lastSearchTime, ddgLastSearch = time.Now(), time.Now()
	t.Cleanup(func() { searchCooldown, ddgCooldown = 0, 0 })

	start := time.Now()
	if _, err := newTool(srv, nil, "", "duckduckgo").Execute(context.Background(), args(t, map[string]any{"query": "q"})); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 30*time.Millisecond {
		t.Error("expected the cooldown to be honoured")
	}
}

func TestDuckDuckGo_RetriesThenSucceeds(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(202)
			return
		}
		_, _ = w.Write([]byte(liteSample))
	}))
	t.Cleanup(srv.Close)
	// Non-zero backoff so the cap branch (backoff > ddgMaxBackoff) runs.
	ddgBackoff, ddgMaxBackoff = time.Millisecond, time.Millisecond
	t.Cleanup(func() { ddgBackoff, ddgMaxBackoff = 0, 0 })

	got, err := newTool(srv, nil, "", "duckduckgo").Execute(context.Background(), args(t, map[string]any{"query": "q"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "go.dev") || n.Load() != 3 {
		t.Errorf("got %q after %d attempts", got, n.Load())
	}
}
