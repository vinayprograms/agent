# Chapter 3: LLM Integration

## Stack

| Library | Purpose |
|---------|---------|
| [Fantasy](https://charm.land/fantasy) | Provider abstraction |

## Supported Providers

| Provider | Model Patterns | Environment Variable |
|----------|---------------|---------------------|
| Anthropic | claude-* | ANTHROPIC_API_KEY |
| OpenAI | gpt-*, o1-*, o3-* | OPENAI_API_KEY |
| Google | gemini-*, gemma-* | GOOGLE_API_KEY |
| Mistral | mistral-*, mixtral-*, codestral-* | MISTRAL_API_KEY |
| Groq | (set provider explicitly) | GROQ_API_KEY |

## Automatic Provider Inference

The `provider` field is optional for standard models. The agent determines the provider using pattern inference from model name prefixes:
- `claude-*` → anthropic
- `gpt-*`, `o1-*`, `o3-*` → openai
- `gemini-*`, `gemma-*` → google
- `mistral-*`, `mixtral-*`, `codestral-*` → mistral

Set `provider` explicitly for ambiguous model names or OpenAI-compatible endpoints.

## Configuration

```toml
# agent.toml

[llm]
model = "claude-sonnet-4-20250514"
max_tokens = 4096  # Required
# provider = "anthropic"  # Optional, inferred from model

# Custom endpoint
# base_url = "http://localhost:8080/v1"
```

### Required Fields

| Field | Description |
|-------|-------------|
| `model` | Model identifier (e.g., "claude-sonnet-4-20250514") |
| `max_tokens` | Maximum tokens for response generation |

### Optional Fields

| Field | Description |
|-------|-------------|
| `provider` | Provider name (inferred from model if not set) |
| `base_url` | Custom endpoint URL for OpenAI-compatible services |

## Custom Endpoints

Use `base_url` for OpenAI-compatible endpoints:

```toml
# OpenRouter
[llm]
provider = "openrouter"
model = "anthropic/claude-3.5-sonnet"
base_url = "https://openrouter.ai/api/v1"

# Ollama
[llm]
provider = "ollama"
model = "llama3:70b"
base_url = "http://localhost:11434/v1"

# LiteLLM proxy
[llm]
provider = "litellm"
model = "gpt-4"
base_url = "http://localhost:4000"
```

## Credentials

```toml
# ~/.config/grid/credentials.toml

# Simple: single key for all providers
[llm]
api_key = "your-api-key"

# Or provider-specific
[anthropic]
api_key = "anthropic-key"

[openai]
api_key = "openai-key"
```

**Priority:** Provider section → [llm] section → environment variable

**Permissions:** File must be mode 0400 (owner read-only). Agent refuses to load insecure credentials.

## Embedding Models

For semantic memory, configure an embedding provider:

```toml
# agent.toml

[embedding]
provider = "ollama"                 # "openai", "ollama", or "none"
model = "nomic-embed-text"
base_url = "http://localhost:11434" # For ollama or custom endpoints
```

| Provider | Models | Dimension | Notes |
|----------|--------|-----------|-------|
| `none` | — | — | Disables semantic memory |
| OpenAI | text-embedding-3-small | 1536 | Fast, good quality |
| OpenAI | text-embedding-3-large | 3072 | Higher quality |
| Ollama | nomic-embed-text | 768 | Local, requires ~1GB RAM |
| Ollama | mxbai-embed-large | 1024 | Local, requires ~2GB RAM |

Credentials for embeddings use the same `credentials.toml` priority.

For resource-constrained environments (small VPS, limited RAM), use `provider = "none"` to disable semantic memory. The agent will still have KV (photographic) memory.

## Catwalk Benefits

When using Catwalk:
- Model context windows and token limits
- Cost information
- Capability flags (reasoning, attachments)

Run a local Catwalk server:
```bash
export CATWALK_URL=http://localhost:8080
```

---

Next: [Tool System](04-tools.md)
