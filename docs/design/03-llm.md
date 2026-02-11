# Chapter 3: LLM Integration

## Stack

| Library | Purpose |
|---------|---------|
| [anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go) | Official Anthropic SDK |
| [openai-go](https://github.com/openai/openai-go) | Official OpenAI SDK |
| [generative-ai-go](https://github.com/google/generative-ai-go) | Official Google Gemini SDK |
| Native HTTP | OpenAI-compatible APIs (Mistral, Groq, etc.) |

## Supported Providers

| Provider | Model Patterns | Environment Variable |
|----------|---------------|---------------------|
| Anthropic | claude-* | ANTHROPIC_API_KEY |
| OpenAI | gpt-*, o1-*, o3-* | OPENAI_API_KEY |
| Google | gemini-*, gemma-* | GOOGLE_API_KEY |
| Mistral | mistral-*, mixtral-*, codestral-* | MISTRAL_API_KEY |
| Groq | (set provider explicitly) | GROQ_API_KEY |
| xAI | grok-* | XAI_API_KEY |
| OpenRouter | (set provider explicitly) | OPENROUTER_API_KEY |
| Ollama Cloud | (set provider explicitly) | OLLAMA_API_KEY |
| Ollama Local | (set provider explicitly) | (none required) |
| LMStudio | (set provider explicitly) | (none required) |

## Automatic Provider Inference

The `provider` field is optional for standard models. The agent determines the provider using pattern inference from model name prefixes:
- `claude-*` → anthropic
- `gpt-*`, `o1-*`, `o3-*` → openai
- `gemini-*`, `gemma-*` → google
- `mistral-*`, `mixtral-*`, `codestral-*` → mistral
- `grok-*` → xai

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

## Custom Endpoints (OpenAI-Compatible)

Any service implementing the OpenAI chat completions API can be used with `provider = "openai-compat"`:

```toml
[llm]
provider = "openai-compat"
model = "your-model-name"
max_tokens = 4096
base_url = "https://your-service.com/v1"
```

### Built-in OpenAI-Compatible Providers

These have default base URLs and can be used without specifying `base_url`:

| Provider | Default Base URL | API Key Env |
|----------|-----------------|-------------|
| `groq` | api.groq.com/openai/v1 | GROQ_API_KEY |
| `mistral` | api.mistral.ai/v1 | MISTRAL_API_KEY |
| `xai` | api.x.ai/v1 | XAI_API_KEY |
| `openrouter` | openrouter.ai/api/v1 | OPENROUTER_API_KEY |
| `ollama-local` | localhost:11434/v1 | (none) |
| `lmstudio` | localhost:1234/v1 | (none) |

### Examples

```toml
# OpenRouter - route to any model
[llm]
provider = "openrouter"
model = "anthropic/claude-3.5-sonnet"
max_tokens = 4096

# xAI (Grok)
[llm]
provider = "xai"
model = "grok-2"
max_tokens = 4096

# Local Ollama
[llm]
provider = "ollama-local"
model = "llama3:70b"
max_tokens = 4096

# LMStudio local server
[llm]
provider = "lmstudio"
model = "loaded-model"
max_tokens = 4096

# LiteLLM proxy
[llm]
provider = "openai-compat"
model = "gpt-4"
max_tokens = 4096
base_url = "http://localhost:4000"

# vLLM server
[llm]
provider = "openai-compat"
model = "meta-llama/Llama-3-70b"
max_tokens = 4096
base_url = "http://localhost:8000/v1"

# Together AI
[llm]
provider = "openai-compat"
model = "meta-llama/Llama-3-70b-chat-hf"
max_tokens = 4096
base_url = "https://api.together.xyz/v1"

# Anyscale
[llm]
provider = "openai-compat"
model = "meta-llama/Llama-3-70b-chat-hf"
max_tokens = 4096
base_url = "https://api.endpoints.anyscale.com/v1"

# Fireworks AI
[llm]
provider = "openai-compat"
model = "accounts/fireworks/models/llama-v3-70b-instruct"
max_tokens = 4096
base_url = "https://api.fireworks.ai/inference/v1"

# Perplexity
[llm]
provider = "openai-compat"
model = "llama-3.1-sonar-large-128k-online"
max_tokens = 4096
base_url = "https://api.perplexity.ai"

# DeepSeek
[llm]
provider = "openai-compat"
model = "deepseek-chat"
max_tokens = 4096
base_url = "https://api.deepseek.com/v1"
```

### Adding New Providers

Any OpenAI-compatible service works. The agent sends:

```http
POST {base_url}/chat/completions
Authorization: Bearer {api_key}
Content-Type: application/json

{
  "model": "...",
  "messages": [...],
  "max_tokens": ...,
  "tools": [...]  // if tools defined
}
```

Expected response format:
```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "...",
      "tool_calls": [...]
    },
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": ...,
    "completion_tokens": ...
  }
}
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

---

Next: [Tool System](04-tools.md)
