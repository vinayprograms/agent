# Semantic Memory System

The agent includes a memory system with two components:

- **Photographic memory (KV)** — exact key-value recall, stored as JSON
- **Semantic memory** — insights and decisions, stored with BM25 full-text search and semantic query expansion

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Memory System                             │
│       (persist_memory=true: shared across runs)              │
│       (persist_memory=false: session-scoped)                 │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────────┐    ┌─────────────────────────────────┐    │
│  │ Photographic │    │      Semantic Memory            │    │
│  │   (kv.json)  │    │                                 │    │
│  │              │    │  observations.bleve/  (BM25)    │    │
│  │  Exact keys  │    │  semantic_graph.json            │    │
│  │  Fast lookup │    │                                 │    │
│  └──────────────┘    │  ┌─────────────────────────┐    │    │
│                      │  │   Query Expansion       │    │    │
│                      │  │   user → person, owner  │    │    │
│                      │  │   fast → speed, quick   │    │    │
│                      │  └─────────────────────────┘    │    │
│                      └─────────────────────────────────┘    │
│         │                           │                        │
│         ▼                           ▼                        │
│  ┌────────────────────────────────────────────────────┐     │
│  │   memory_read/write      memory_recall/remember    │     │
│  └────────────────────────────────────────────────────┘     │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

## Why BM25 + Semantic Graph (not SQLite-vec)

The memory system uses **pure Go** with no CGO dependencies:

| Approach | Pros | Cons |
|----------|------|------|
| SQLite-vec | True vector similarity | Requires CGO, complex build |
| **BM25 + Graph** | CGO-free, cross-compile, fast | Query expansion instead of dense vectors |

**How it works:**
1. **BM25 (Bleve)** — O(log n) inverted index for full-text search
2. **Semantic Graph** — Stores term→synonyms relationships with embeddings
3. **Query Expansion** — "fast" → search for "fast OR speed OR quick"

Embeddings are only generated at index time (when new unique terms appear), never at query time. This means:
- Provider outages don't block search
- 1 API call per new unique term (not per query)
- Graph auto-rebuilds if embedding provider/model changes

## Configuration

```toml
# Embedding providers for semantic graph (query expansion).
#
# Supported:
#   - openai:  text-embedding-3-small, text-embedding-3-large
#   - google:  text-embedding-004, embedding-001
#   - mistral: mistral-embed
#   - cohere:  embed-english-v3.0, embed-multilingual-v3.0
#   - voyage:  voyage-2, voyage-large-2, voyage-code-2
#   - ollama-cloud: nomic-embed-text, mxbai-embed-large
#   - none:    Disables semantic graph (BM25 only, no expansion)
#
# NOT supported (no embedding endpoints):
#   - anthropic (Claude) - use voyage instead
#   - openrouter - chat completions only
#   - groq - chat completions only
#
[embedding]
provider = "openai"
model = "text-embedding-3-small"
# base_url = "https://custom-endpoint.com"  # optional

[storage]
path = "~/.local/grid"              # Base directory for all persistent data
persist_memory = true               # true = survives across runs
                                    # false = in-memory only (scratchpad)
```

## Directory Structure

When `persist_memory = true`:

```
{storage.path}/
├── sessions/               # Session state (execution trace, checkpoints)
├── kv.json                 # Photographic memory (key-value)
├── observations.bleve/     # BM25 index directory
├── semantic_graph.json     # Term relationships + embeddings
└── logs/                   # Audit logs
```

When `persist_memory = false`:

```
{storage.path}/
├── sessions/           # Session state (still persisted)
└── logs/               # Audit logs (still persisted)

# kv.json, observations.bleve, and semantic_graph.json are NOT written
# Memory is held in-memory for the duration of the run
```

## Behavior by Mode

| `persist_memory` | KV Store | Semantic Store | Use Case |
|------------------|----------|----------------|----------|
| `true` | `kv.json` on disk | BM25 + graph on disk | Personal assistant, long-running agent |
| `false` | In-memory map | In-memory index | Task runner, enterprise (uses MCP for memory) |

When `persist_memory = false`, memory still works within a single run — useful for multi-step workflows where earlier insights inform later goals.

## Memory Tools

### Photographic Memory (KV)

Exact key-value storage for precise recall:

```
memory_write(key: "api_endpoint", value: "https://api.acme.com/v2")
memory_read(key: "api_endpoint")  → "https://api.acme.com/v2"
memory_list()                     → ["api_endpoint", ...]
memory_search("acme")             → finds keys/values containing "acme"
```

### Semantic Memory

Meaning-based storage for insights and decisions:

```
memory_remember(
  content: "We decided to use PostgreSQL for better JSON support",
  importance: 0.8,  # 0.0-1.0, default 0.5
  tags: ["architecture", "database"]
)

memory_recall(
  query: "database decision",
  limit: 5  # default 5
)
```

Returns memories ranked by relevance:
```json
[
  {
    "id": "abc-123",
    "content": "We decided to use PostgreSQL for better JSON support",
    "score": 0.87,
    "tags": ["architecture", "database"]
  }
]
```

```
memory_forget(id: "abc-123")
```

## How Semantic Search Works

1. Query is tokenized into keywords
2. Keywords are expanded via semantic graph (fast → speed, quick, performance)
3. BM25 search runs on expanded query
4. Results are ranked by BM25 score (normalized to 0-1)

The semantic graph builds relationships by:
1. Extracting keywords from stored content
2. Generating embeddings for new terms
3. Finding similar terms via cosine similarity
4. Storing relationships as graph edges

This means:
- "database decision" finds "We chose PostgreSQL" via keyword expansion
- No embedding API call at query time
- Graph improves as more content is stored

## Observation Extraction

When `small_llm` is configured, the agent automatically extracts observations from step outputs:

```toml
[small_llm]
provider = "openai"
model = "gpt-4o-mini"
max_tokens = 1024
```

After each GOAL/AGENT step:
1. Output is sent to small_llm for extraction
2. LLM extracts findings, insights, lessons, keywords
3. Stored with importance weighting:
   - **Findings** (0.7): Facts discovered
   - **Insights** (0.8): Conclusions drawn
   - **Lessons** (0.9): Guidance for future work
4. Keywords enrich the semantic graph

This enables the agent to learn from its own work automatically.

## When to Use Which

| Need | Tool | Example |
|------|------|---------|
| Exact value lookup | `memory_read` | API keys, endpoints, IDs |
| "What did we decide about X?" | `memory_recall` | Architecture decisions |
| Store a preference | `memory_write` | `theme = "dark"` |
| Store an insight | `memory_remember` | "User prefers terse responses" |

**Photographic (KV):** When you need the exact value back, no fuzziness.

**Semantic:** When you want to find relevant context by meaning.

## Enterprise Deployment

For multi-tenant deployments, disable local memory and use MCP tools:

```toml
[storage]
path = "~/.local/grid"
persist_memory = false              # Local memory disabled

[mcp.servers.company_memory]
command = "company-memory-mcp"

[mcp.servers.user_memory]
command = "user-memory-mcp"
```

MCP servers handle tenant isolation, embeddings, and routing to appropriate memory tiers (product/company/user).

## Semantic Graph Schema

### semantic_graph.json

```json
{
  "meta": {
    "provider": "openai",
    "model": "text-embedding-3-small",
    "base_url": "https://api.openai.com/v1",
    "dimensions": 1536,
    "built_at": "2026-02-11T04:16:00Z"
  },
  "terms": {
    "fast": {
      "embedding": [0.12, -0.34, ...],
      "related": ["speed", "performance", "quick"]
    },
    "database": {
      "embedding": [0.56, 0.78, ...],
      "related": ["postgresql", "storage", "schema"]
    }
  }
}
```

When the embedding provider/model changes, the graph auto-rebuilds from stored observations.

## Embedding Providers

| Provider | Models | Dimension | Notes |
|----------|--------|-----------|-------|
| `none` | — | — | Disables query expansion (BM25 only) |
| `openai` | text-embedding-3-small | 1536 | Fast, good quality |
| `openai` | text-embedding-3-large | 3072 | Higher quality |
| `google` | text-embedding-004 | 768 | Gemini embeddings |
| `mistral` | mistral-embed | 1024 | Mistral's embedding model |
| `cohere` | embed-english-v3.0 | 1024 | Good for English text |
| `cohere` | embed-multilingual-v3.0 | 1024 | Multi-language support |
| `voyage` | voyage-2, voyage-large-2 | 1024 | Anthropic's recommended partner |
| `voyage` | voyage-code-2 | 1536 | Optimized for code |
| `ollama-cloud` | nomic-embed-text | 768 | Via Ollama Cloud API |
| `ollama-cloud` | mxbai-embed-large | 1024 | Higher quality |

**Providers without embedding endpoints:**
- Anthropic (Claude) — Use `voyage` instead (Anthropic's official recommendation)
- OpenRouter — Chat completions only
- Groq — Chat completions only

### Disabling Semantic Graph

For resource-constrained environments, disable the semantic graph:

```toml
[embedding]
provider = "none"
```

The agent will still have:
- Photographic memory (KV store)
- BM25 full-text search (no query expansion)

## Best Practices

1. **Check memory early** — Use `memory_recall` at the start of complex tasks
2. **Store conclusions, not raw data** — Distill insights before storing
3. **Use meaningful importance scores** — 0.8-1.0 for critical decisions
4. **Tag for organization** — Makes filtering easier later
5. **Make content self-contained** — Should make sense without context
6. **KV for structured, semantic for unstructured** — Don't mix them up
7. **Configure small_llm** — Enables automatic observation extraction
