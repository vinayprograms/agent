# Supported LLM Providers

The agent uses official SDKs where available and native HTTP for OpenAI-compatible endpoints.

## Provider Overview

| Provider | SDK | Notes |
|----------|-----|-------|
| Anthropic | [anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go) | Official SDK |
| OpenAI | [openai-go](https://github.com/openai/openai-go) | Official SDK |
| Google | [generative-ai-go](https://github.com/google/generative-ai-go) | Official Gemini SDK |
| Mistral | Native HTTP | OpenAI-compatible API |
| Groq | Native HTTP | OpenAI-compatible API |
| xAI | Native HTTP | OpenAI-compatible API |
| OpenRouter | Native HTTP | OpenAI-compatible API |
| Ollama Cloud | Native HTTP | Native Ollama API |
| Ollama Local | Native HTTP | OpenAI-compatible API |
| LMStudio | Native HTTP | OpenAI-compatible API |

## Configuration Reference

| Provider | `provider` value | `api_key_env` | Notes |
|----------|-----------------|---------------|-------|
| Anthropic | `anthropic` | `ANTHROPIC_API_KEY` | Claude models |
| OpenAI | `openai` | `OPENAI_API_KEY` | GPT, o1, o3 models |
| Google | `google` | `GOOGLE_API_KEY` | Gemini models |
| Groq | `groq` | `GROQ_API_KEY` | Fast inference |
| Mistral | `mistral` | `MISTRAL_API_KEY` | Mistral models |
| xAI | `xai` | `XAI_API_KEY` | Grok models |
| OpenRouter | `openrouter` | `OPENROUTER_API_KEY` | Multi-provider routing |
| Ollama Local | `ollama-local` | — | Local, no API key |
| LMStudio | `lmstudio` | — | Local, no API key |
| Ollama Cloud | `ollama-cloud` | `OLLAMA_API_KEY` | Hosted models |
| Generic | `openai-compat` | — | Any OpenAI-compatible |

## Model Discovery

The `provider` field is **optional** for standard models — the agent automatically determines the provider using pattern inference from model name prefixes:

- `claude-*` -> anthropic
- `gpt-*`, `o1-*`, `o3-*` -> openai
- `gemini-*`, `gemma-*` -> google
- `mistral-*`, `mixtral-*`, `codestral-*` -> mistral
- `grok-*` -> xai

Set `provider` explicitly for custom or ambiguous model names.

## Required Configuration

| Field | Description |
|-------|-------------|
| `model` | Model identifier (e.g., "claude-sonnet-4-20250514") |
| `max_tokens` | Maximum tokens for response generation |

## OpenAI-Compatible Endpoints

Any service implementing the OpenAI chat completions API can be used. Built-in providers have default base URLs:

| Provider | Default Base URL |
|----------|-----------------|
| `groq` | api.groq.com/openai/v1 |
| `mistral` | api.mistral.ai/v1 |
| `xai` | api.x.ai/v1 |
| `openrouter` | openrouter.ai/api/v1 |
| `ollama-local` | localhost:11434/v1 |
| `lmstudio` | localhost:1234/v1 |

For services without built-in support, use `openai-compat` with a custom `base_url`:

```toml
# Together AI
[llm]
provider = "openai-compat"
model = "meta-llama/Llama-3-70b-chat-hf"
max_tokens = 4096
base_url = "https://api.together.xyz/v1"

# Fireworks AI
[llm]
provider = "openai-compat"
model = "accounts/fireworks/models/llama-v3-70b-instruct"
max_tokens = 4096
base_url = "https://api.fireworks.ai/inference/v1"

# Anyscale
[llm]
provider = "openai-compat"
model = "meta-llama/Llama-3-70b-chat-hf"
max_tokens = 4096
base_url = "https://api.endpoints.anyscale.com/v1"

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

# vLLM self-hosted
[llm]
provider = "openai-compat"
model = "meta-llama/Llama-3-70b"
max_tokens = 4096
base_url = "http://localhost:8000/v1"

# LiteLLM proxy
[llm]
provider = "openai-compat"
model = "gpt-4"
max_tokens = 4096
base_url = "http://localhost:4000"
```

## Provider Examples

```toml
# xAI (Grok)
[llm]
provider = "xai"
model = "grok-2"
max_tokens = 4096

# OpenRouter - route to any model
[llm]
provider = "openrouter"
model = "anthropic/claude-3.5-sonnet"
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
```

## Ollama Cloud

Ollama Cloud provides hosted models accessible via API. Use the native `ollama-cloud` provider:

```toml
# agent.toml
[llm]
provider = "ollama-cloud"
model = "gpt-oss:120b"  # or any model from ollama.com/search?c=cloud
```

```toml
# credentials.toml
[ollama-cloud]
api_key = "your-ollama-api-key"  # from ollama.com/settings/keys
```

Available models: [ollama.com/search?c=cloud](https://ollama.com/search?c=cloud)

**Note:** This is different from local Ollama. For local Ollama, use `provider = "ollama"` with `base_url = "http://localhost:11434/v1"`.

## Proxying Native Providers

You can also use `base_url` with native providers to proxy requests:

```toml
[llm]
provider = "openai"
model = "gpt-4"
base_url = "https://my-company-proxy.com/v1"
```

---

Back to [README](../../README.md) | See also: [Profiles](profiles.md), [Thinking](thinking.md)
