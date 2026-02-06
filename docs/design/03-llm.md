# Chapter 3: LLM Integration

## Stack

| Library | Purpose |
|---------|---------|
| [Catwalk](https://github.com/charmbracelet/catwalk) | Model discovery and metadata |
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

The `provider` field is optional. The agent determines the provider:

1. **Catwalk lookup** (if CATWALK_URL set): Queries for exact model → provider mapping
2. **Pattern inference** (fallback): Uses model name prefixes

Set `provider` explicitly only for ambiguous model names or OpenAI-compatible endpoints.

## Configuration

```toml
# agent.toml

[llm]
model = "claude-sonnet-4-20250514"
max_tokens = 4096
# provider = "anthropic"  # Optional, inferred from model

# Custom endpoint
# base_url = "http://localhost:8080/v1"
```

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
