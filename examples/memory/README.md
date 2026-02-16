# Memory Examples

This directory contains examples demonstrating the semantic memory system.

## Prerequisites

Enable memory in your `agent.toml`:

```toml
[memory]
enabled = true
path = "~/.agent/memory.db"  # optional, this is the default

[memory.embedding]
provider = "openai"  # or "ollama"
model = "text-embedding-3-small"  # optional, this is the default
```

For Ollama:
```toml
[memory.embedding]
provider = "ollama"
model = "nomic-embed-text"
base_url = "http://localhost:11434"  # optional, this is the default
```

## Examples

### simple-memory.agent

Basic demonstration of the three memory tools:
- `remember` - Store insights semantically
- `recall` - Search for relevant memories
- `memory_forget` - Delete memories by ID

```bash
agent run examples/memory/simple-memory.agent
```

### research-with-memory.agent

Shows how memory enhances research workflows:
- Check prior knowledge before starting
- Build on existing context
- Store new insights for future sessions

```bash
agent run examples/memory/research-with-memory.agent topic="machine learning"
```

## How It Works

### Semantic Storage

Unlike key-value storage, semantic memory uses embeddings to store content:
- Content is converted to a vector embedding
- Stored in SQLite with sqlite-vec for vector search
- Queries find semantically similar content, not just keyword matches

### Memory Tools

| Tool | Purpose |
|------|---------|
| `remember` | Store content with importance and tags |
| `recall` | Semantic search for relevant memories |
| `memory_forget` | Delete a memory by ID |
| `memory_read` | Get value by exact key (legacy) |
| `memory_write` | Store key-value pair (legacy) |
| `memory_list` | List keys by prefix (legacy) |
| `memory_search` | Substring search (legacy) |

### Consolidation

At the end of each session, the agent automatically extracts key insights from the conversation and stores them. This happens without explicit `remember` calls.

Consolidated memories are tagged with `source: session:<id>` for traceability.

## Best Practices

1. **Use recall early** - Check for relevant context before starting complex tasks
2. **Store distilled insights** - Don't store raw data, store conclusions and decisions
3. **Use meaningful content** - Memories should be self-contained and understandable later
4. **Tag appropriately** - Tags help with filtering and organization
5. **Set importance scores** - Higher scores (0.8-1.0) for critical decisions
