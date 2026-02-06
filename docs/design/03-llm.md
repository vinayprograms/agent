# Chapter 3: LLM Integration

## Fantasy Framework

The agent uses [charm.land/fantasy](https://charm.land/fantasy) for LLM abstraction. Fantasy provides a unified interface across providers.

## Supported Providers

| Provider | Models | Environment Variable |
|----------|--------|---------------------|
| Anthropic | Claude family | ANTHROPIC_API_KEY |
| OpenAI | GPT family | OPENAI_API_KEY |
| Google | Gemini family | GOOGLE_API_KEY |
| Groq | Llama, Mixtral | GROQ_API_KEY |
| Mistral | Mistral family | MISTRAL_API_KEY |

Any OpenAI-compatible endpoint also works (OpenRouter, LiteLLM, Ollama, LMStudio).

## Automatic Provider Inference

The agent infers the provider from the model name:

| Model Pattern | Provider |
|---------------|----------|
| claude-* | Anthropic |
| gpt-* | OpenAI |
| gemini-* | Google |
| llama-*, mixtral-* | Groq |
| mistral-*, codestral-* | Mistral |

For ambiguous names, specify the provider explicitly in configuration.

## Configuration

### agent.toml

```toml
[llm]
model = "claude-sonnet"
# provider = "anthropic"  # Optional, inferred from model name

# For custom endpoints (proxies, self-hosted)
# base_url = "http://localhost:8080/v1"
```

### credentials.toml

```toml
[llm]
api_key = "your-api-key"
```

This single key works for any provider. For multiple providers:

```toml
[anthropic]
api_key = "sk-ant-..."

[openai]
api_key = "sk-..."
```

**Permissions:** credentials.toml must be 0400 (read-only, owner only).

## Credential Priority

1. Provider-specific section in credentials.toml
2. `[llm]` section in credentials.toml
3. Environment variable (ANTHROPIC_API_KEY, etc.)

File takes precedence over environment.

## Catwalk Integration

Optional integration with [charm.land/catwalk](https://charm.land/catwalk) for model discovery and metadata:

```toml
[llm]
use_catwalk = true
```

When enabled, catwalk provides:
- Available models list
- Context window sizes
- Pricing information
- Capability flags

## Rate Limiting

The agent handles rate limits with exponential backoff:

| Attempt | Delay |
|---------|-------|
| 1 | 1 second |
| 2 | 2 seconds |
| 3 | 4 seconds |
| 4 | 8 seconds |
| 5 | 16 seconds |

After 5 attempts, the request fails.

**Billing errors are fatal** — no retry on payment/quota issues.

---

Next: [Tool System](04-tools.md)
