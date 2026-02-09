# Semantic Memory System

The agent includes a memory system with two components:

- **Photographic memory (KV)** — exact key-value recall, stored as JSON
- **Semantic memory** — insights and decisions, stored with vector embeddings for meaning-based search

## Architecture

```
┌─────────────────────────────────────────────────┐
│            Memory System                         │
│    (persist_memory=true: shared across runs)     │
│    (persist_memory=false: session-scoped)        │
├─────────────────────────────────────────────────┤
│                                                  │
│  ┌──────────────┐    ┌──────────────────────┐   │
│  │ Photographic │    │   Semantic           │   │
│  │   (kv.json)  │    │   (semantic.db)      │   │
│  │              │    │                      │   │
│  │  Exact keys  │    │  Vector embeddings   │   │
│  │  Fast lookup │    │  Meaning-based search│   │
│  └──────────────┘    └──────────────────────┘   │
│         │                      │                 │
│         ▼                      ▼                 │
│  ┌──────────────────────────────────────────┐   │
│  │   memory_get/set    memory_recall/note   │   │
│  └──────────────────────────────────────────┘   │
│                                                  │
└─────────────────────────────────────────────────┘
```

## Configuration

```toml
[embedding]
provider = "ollama"                 # "openai" or "ollama"
model = "nomic-embed-text"
base_url = "http://localhost:11434" # For ollama or custom endpoints

[storage]
path = "~/.local/grid"              # Base directory for all persistent data
persist_memory = true               # true = survives across runs
                                    # false = in-memory only (scratchpad)
```

## Directory Structure

When `persist_memory = true`:

```
{storage.path}/
├── sessions/           # Session state (execution trace, checkpoints)
├── kv.json             # Photographic memory (key-value)
├── semantic.db         # Semantic memory (sqlite-vec)
└── logs/               # Audit logs
```

When `persist_memory = false`:

```
{storage.path}/
├── sessions/           # Session state (still persisted)
└── logs/               # Audit logs (still persisted)

# kv.json and semantic.db are NOT written
# Memory is held in-memory for the duration of the run
```

## Behavior by Mode

| `persist_memory` | KV Store | Semantic Store | Use Case |
|------------------|----------|----------------|----------|
| `true` | `kv.json` on disk | `semantic.db` on disk | Personal assistant, long-running agent |
| `false` | In-memory map | In-memory index | Task runner, enterprise (uses MCP for memory) |

When `persist_memory = false`, memory still works within a single run — useful for multi-step workflows where earlier insights inform later goals.

## Memory Tools

### Photographic Memory (KV)

Exact key-value storage for precise recall:

```
memory_set(key: "api_endpoint", value: "https://api.acme.com/v2")
memory_get(key: "api_endpoint")  → "https://api.acme.com/v2"
memory_list()                    → ["api_endpoint", ...]
memory_delete(key: "api_endpoint")
```

### Semantic Memory

Meaning-based storage for insights and decisions:

```
memory_note(
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

1. Content is converted to a vector embedding using the configured provider
2. Embeddings are stored in SQLite using the sqlite-vec extension
3. Queries are also converted to embeddings
4. Vector similarity search finds the most relevant memories
5. Results are ranked by similarity score (0-1)

This means:
- "database decision" finds "We chose PostgreSQL" even without exact keyword match
- "user preferences" finds "Dark mode and vim keybindings"
- Semantic similarity, not substring matching

## When to Use Which

| Need | Tool | Example |
|------|------|---------|
| Exact value lookup | `memory_get` | API keys, endpoints, IDs |
| "What did we decide about X?" | `memory_recall` | Architecture decisions |
| Store a preference | `memory_set` | `theme = "dark"` |
| Store an insight | `memory_note` | "User prefers terse responses" |

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

## Database Schema

### kv.json

```json
{
  "api_endpoint": "https://api.acme.com/v2",
  "preferred_format": "json",
  "last_deploy": "2026-02-09T04:00:00Z"
}
```

### semantic.db

```sql
CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    source TEXT,           -- "session:xyz", "explicit"
    importance REAL,       -- 0.0-1.0
    created_at DATETIME,
    accessed_at DATETIME,
    access_count INTEGER,
    tags TEXT              -- JSON array
);

CREATE VIRTUAL TABLE memory_vectors USING vec0(
    id TEXT PRIMARY KEY,
    embedding FLOAT[768]   -- dimension depends on model
);
```

## Embedding Providers

| Provider | Models | Dimension | Notes |
|----------|--------|-----------|-------|
| `none` | — | — | Disables semantic memory (KV only) |
| OpenAI | text-embedding-3-small | 1536 | Fast, good quality |
| OpenAI | text-embedding-3-large | 3072 | Higher quality |
| Ollama | nomic-embed-text | 768 | Local, no API calls |
| Ollama | mxbai-embed-large | 1024 | Local, higher quality |

### Disabling Semantic Memory

For resource-constrained environments, disable semantic memory entirely:

```toml
[embedding]
provider = "none"
```

The agent will still have photographic memory (KV store) but `memory_recall` semantic search will be unavailable.

## Best Practices

1. **Check memory early** — Use `memory_recall` at the start of complex tasks
2. **Store conclusions, not raw data** — Distill insights before storing
3. **Use meaningful importance scores** — 0.8-1.0 for critical decisions
4. **Tag for organization** — Makes filtering easier later
5. **Make content self-contained** — Should make sense without context
6. **KV for structured, semantic for unstructured** — Don't mix them up
