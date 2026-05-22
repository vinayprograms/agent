# Web Search

The `web_search` tool supports three backends with automatic fallback. Credentials are resolved from `~/.config/grid/credentials.toml` first, then environment variables.

## Backend Priority

Brave → Tavily → DuckDuckGo (fallback)

| Priority | Provider | Key source | Notes |
|----------|----------|------------|-------|
| 1 | Brave Search | `credentials.toml` or `BRAVE_API_KEY` | Best result quality |
| 2 | Tavily | `credentials.toml` or `TAVILY_API_KEY` | Good for research |
| 3 | DuckDuckGo | None required | Zero-config fallback; rate limited |

## Brave Search

```toml
# ~/.config/grid/credentials.toml
[brave]
api_key = "BSA..."
```

Or set `BRAVE_API_KEY` in the environment.

## Tavily

```toml
[tavily]
api_key = "tvly-..."
```

Or set `TAVILY_API_KEY` in the environment.

## DuckDuckGo

Used automatically when no API key is configured. Subject to rate limiting under concurrent sub-agent workloads.

### Tuning the cooldown

A configurable inter-query cooldown serializes DuckDuckGo requests across all sub-agents in a session and prevents 202 rate-limit responses. The default is 2000 ms.

```toml
# agent.toml
[timeouts]
  search_cooldown_ms = 2000  # milliseconds between DDG queries (default: 2000)
```

Increase this if you still see rate-limit errors with large sub-agent pipelines.

### Session cache

Results from all backends are cached in memory for 5 minutes per session. Sub-agents querying the same or overlapping topics share cached results, reducing total search volume regardless of which backend is active.

---

Back to [README](../../README.md) | See also: [Protocols](protocols.md)
