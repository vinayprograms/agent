# Semantic Memory System

The agent includes a semantic memory system that enables persistent, searchable memory across sessions. Unlike traditional key-value storage, semantic memory uses vector embeddings to find relevant content by meaning.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                  Memory System                   │
├─────────────────────────────────────────────────┤
│                                                  │
│  ┌──────────────┐    ┌──────────────────────┐  │
│  │   Episodic   │───▶│   Consolidation      │  │
│  │  (sessions)  │    │   (end-of-session)   │  │
│  └──────────────┘    └──────────┬───────────┘  │
│                                  │              │
│                                  ▼              │
│  ┌──────────────┐    ┌──────────────────────┐  │
│  │   Semantic   │◀───│   Vector Index       │  │
│  │  (insights)  │    │   (sqlite-vec)       │  │
│  └──────────────┘    └──────────────────────┘  │
│                                  │              │
│                                  ▼              │
│  ┌──────────────────────────────────────────┐  │
│  │         memory_recall / memory_remember  │  │
│  │            (agent-controlled recall)     │  │
│  └──────────────────────────────────────────┘  │
│                                                  │
└─────────────────────────────────────────────────┘
```

## Configuration

```toml
[memory]
enabled = true
path = "~/.agent/memory.db"  # SQLite database path

[memory.embedding]
provider = "openai"              # openai or ollama
model = "text-embedding-3-small" # embedding model
api_key_env = "OPENAI_API_KEY"   # optional, uses credentials.toml by default
```

### Ollama Configuration

```toml
[memory.embedding]
provider = "ollama"
model = "nomic-embed-text"      # or mxbai-embed-large, all-minilm
base_url = "http://localhost:11434"
```

## Memory Tools

### memory_remember

Store content for future sessions:

```
memory_remember(
  content: "We decided to use PostgreSQL for better JSON support",
  importance: 0.8,  # 0.0-1.0, default 0.5
  tags: ["architecture", "database"]
)
```

### memory_recall

Semantic search for relevant memories:

```
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

### memory_forget

Delete a memory by ID:

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

## Automatic Consolidation

At the end of each session, the agent automatically:

1. Scans the conversation for key decision language ("decided", "concluded", "remember", etc.)
2. Extracts significant content
3. Stores it as memories tagged with `source: session:<id>`

This happens without explicit `memory_remember` calls.

## Memory Types

| Type | Storage | Use Case |
|------|---------|----------|
| Semantic | Vector DB | Insights, decisions, knowledge |
| Key-Value | Simple table | Preferences, config, structured data |

Legacy tools (`memory_read`, `memory_write`, `memory_list`, `memory_search`) use key-value storage for backward compatibility.

## Database Schema

```sql
-- Semantic memories with embeddings
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
    embedding FLOAT[1536]  -- dimension depends on model
);

-- Legacy key-value store
CREATE TABLE memory_kv (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at DATETIME
);
```

## Best Practices

1. **Check memory early** - Use `memory_recall` at the start of complex tasks
2. **Store conclusions, not raw data** - Distill insights before storing
3. **Use meaningful importance scores** - 0.8-1.0 for critical decisions
4. **Tag for organization** - Makes filtering easier later
5. **Make content self-contained** - Should make sense without context

## Embedding Providers

| Provider | Models | Dimension | Notes |
|----------|--------|-----------|-------|
| OpenAI | text-embedding-3-small | 1536 | Fast, good quality |
| OpenAI | text-embedding-3-large | 3072 | Higher quality |
| Ollama | nomic-embed-text | 768 | Local, no API calls |
| Ollama | mxbai-embed-large | 1024 | Local, higher quality |
