# Web Search Providers

The `web_search` tool supports multiple backends with automatic fallback.

## Priority Order

SearXNG -> Brave -> Tavily -> DuckDuckGo (fallback)

| Priority | Provider | API Key Required | Notes |
|----------|----------|------------------|-------|
| 1 | SearXNG | None (self-hosted) | Free, no API limits |
| 2 | Brave Search | `BRAVE_API_KEY` | Best quality |
| 3 | Tavily | `TAVILY_API_KEY` | Good for research |
| 4 | DuckDuckGo | None | Zero-config fallback |

Configure in `~/.config/agent/credentials.toml`. You can also pin `web_search`
to a single provider and/or set the SearXNG URL directly in `agent.toml`:

```toml
[web]
search_provider = "searxng"  # "auto" (default), "searxng", "brave", "tavily", "duckduckgo"
searxng_url = "http://localhost:8080"
```

`search_provider` must be one of `""`/`"auto"`, `"searxng"`, `"brave"`,
`"tavily"`, or `"duckduckgo"` — any other value fails config loading with an
error naming the bad value. `[web] searxng_url` takes precedence over the
`[searxng]` credential, which takes precedence over the `SEARXNG_URL`
environment variable.

## SearXNG (Recommended — Free, Self-Hosted)

[SearXNG](https://github.com/searxng/searxng) is a privacy-respecting meta-search engine you can self-host. Zero cost, no API limits.

**Quick setup with Docker:**

```bash
docker run -d --name searxng -p 8080:8080 \
  -v searxng-data:/etc/searxng \
  -e SEARXNG_BASE_URL=http://localhost:8080 \
  searxng/searxng
```

**Configure the agent:**

```toml
# ~/.config/agent/credentials.toml
[searxng]
api_key = "http://localhost:8080"  # This is the URL, not an actual key
```

Or set `SEARXNG_URL=http://localhost:8080` in your environment.

**Security note:** SearXNG has no authentication by default. Either run it on localhost only, or put it behind a VPN/Tailscale.

## Brave Search (Paid)

```toml
[brave]
api_key = "BSA..."
```

## Tavily (Free tier: 1000/month)

```toml
[tavily]
api_key = "tvly-..."
```

## DuckDuckGo (Fallback)

Used automatically if no other provider is configured. Subject to rate limiting.

## Rate Limiting and Caching

DuckDuckGo queries wait out a cooldown between requests to avoid the `202`
rate-limit response. It defaults to 2 seconds and is configurable:

```toml
# agent.toml
[timeouts]
search_cooldown_ms = 2000   # minimum ms between DuckDuckGo queries
```

Raise this if you run several sub-agents concurrently and see `202`
responses; DDG retries a rate-limited request with exponential backoff plus
jitter on top of the cooldown. `search_cooldown_ms = 0` (or omitting the key)
keeps the 2-second default — there is no way to disable the cooldown from
config.

Every `web_search` result (from any provider) is cached in-process, keyed by
provider, query, and result count, for 5 minutes. The cache is shared by all
sub-agents dispatched through the same tool instance, so repeated or
overlapping lookups skip both the cooldown and the HTTP call entirely.

## web_fetch and HTTP/1.1

`web_fetch` forces HTTP/1.1 and sends browser-like headers (`User-Agent`,
`Accept`, `Accept-Language`, etc.). Go's default client negotiates HTTP/2 via
ALPN, and some enterprise CDNs (Akamai, Cloudflare) fingerprint Go's h2
`SETTINGS` frame and reject the connection with `INTERNAL_ERROR`. Forcing
HTTP/1.1 avoids that fingerprint; there is no configuration for this — it is
always on.

---

Back to [README](../../README.md) | See also: [Protocols](protocols.md)
