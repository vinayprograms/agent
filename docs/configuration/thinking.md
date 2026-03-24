# Adaptive Thinking

The agent supports thinking/reasoning modes that enable models to show their work on complex problems. Thinking is controlled per-request using a heuristic classifier — **zero extra LLM calls, just pattern matching**.

## Configuration

```toml
[llm]
model = "claude-sonnet-4-20250514"
thinking = "auto"  # auto|off|low|medium|high
```

| Value | Behavior |
|-------|----------|
| `auto` (default) | Heuristic classifier decides per-request |
| `off` | Never use thinking |
| `low` | Light reasoning (simple questions, few tools) |
| `medium` | Moderate reasoning (code, planning, analysis) |
| `high` | Deep reasoning (proofs, debugging, architecture) |

## How Auto Works

The classifier analyzes each request before sending:

**High complexity** (-> `high`):
- Math expressions, proofs, equations
- "debug", "why is", "root cause"
- "design system", "architecture", "trade-off"
- "security analysis", "threat model"
- Very long context (>3000 chars)
- Many tools (>10)

**Medium complexity** (-> `medium`):
- "implement", "refactor", "optimize"
- "step by step", "explain how"
- Code-related keywords
- Moderate context (>1000 chars)
- Several tools (>5)

**Low complexity** (-> `low`):
- "how to", "what is the best"
- "summarize", "list"
- A few tools (>2)

**Simple queries** (-> `off`):
- Greetings, short questions
- No complexity indicators

## Provider Support

| Provider | Thinking Support | Notes |
|----------|-----------------|-------|
| Anthropic | Extended thinking | Budget tokens auto-scaled by level |
| OpenAI | Reasoning effort | For o1/o3 models |
| Ollama Cloud | Think API | GPT-OSS uses levels, others use bool |
| Google | Not yet supported | |
| Groq/Mistral | Not yet supported | |

---

Back to [README](../../README.md) | See also: [Profiles](profiles.md), [LLM Providers](llm-providers.md)
