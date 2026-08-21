package websearch

import (
	"context"
	"strings"
	"testing"
)

// Representative fragment of a lite.duckduckgo.com response. Attribute order
// (href before class) and the //duckduckgo.com/l/?uddg= redirect wrapper mirror
// the real markup.
const liteSample = `
<table>
<tr><td>1.&nbsp;</td><td>
  <a rel="nofollow" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&amp;rut=abc" class="result-link">The Go Programming Language &amp; Docs</a>
</td></tr>
<tr><td class="result-snippet">Documentation for the <b>Go</b> language.</td></tr>
<tr><td>2.&nbsp;</td><td>
  <a rel="nofollow" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2F&amp;rut=def" class="result-link">pkg.go.dev</a>
</td></tr>
<tr><td class="result-snippet">Go package index.</td></tr>
</table>`

func TestParseDuckDuckGoLite(t *testing.T) {
	results := parseDuckDuckGoLite(liteSample, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}

	if got, want := results[0].URL, "https://go.dev/doc/"; got != want {
		t.Errorf("result[0].URL = %q, want %q", got, want)
	}
	if got, want := results[0].Title, "The Go Programming Language & Docs"; got != want {
		t.Errorf("result[0].Title = %q, want %q", got, want)
	}
	if got, want := results[0].Snippet, "Documentation for the Go language."; got != want {
		t.Errorf("result[0].Snippet = %q, want %q", got, want)
	}
	if got, want := results[1].URL, "https://pkg.go.dev/"; got != want {
		t.Errorf("result[1].URL = %q, want %q", got, want)
	}
}

func TestParseDuckDuckGoLite_RespectsCount(t *testing.T) {
	if got := parseDuckDuckGoLite(liteSample, 1); len(got) != 1 {
		t.Fatalf("expected 1 result with count=1, got %d", len(got))
	}
}

func TestUnwrapDDGRedirect(t *testing.T) {
	cases := map[string]string{
		"//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa&rut=x": "https://example.com/a",
		"https://example.com/plain":                                    "https://example.com/plain",
	}
	for in, want := range cases {
		if got := unwrapDDGRedirect(in); got != want {
			t.Errorf("unwrapDDGRedirect(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNew_ProviderDefaultsToAuto(t *testing.T) {
	if tool := New(nil, "", ""); tool.provider != "auto" {
		t.Errorf("provider = %q, want auto", tool.provider)
	}
}

func TestAutoFallbackErrorMessageIsActionable(t *testing.T) {
	tool := New(nil, "", "")
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "test query that will not match anything",
	})
	if err == nil {
		return
	}
	msg := err.Error()
	for _, want := range []string{"searxng_url", "Brave/Tavily", "DuckDuckGo"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message should mention %q, got: %s", want, msg)
		}
	}
}
