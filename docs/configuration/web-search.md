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

Configure in `~/.config/grid/credentials.toml`.

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
# ~/.config/grid/credentials.toml
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

---

Back to [README](../../README.md) | See also: [Protocols](protocols.md)
