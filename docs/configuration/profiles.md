# Capability Profiles

Agents can declare capability requirements using `REQUIRES`. The config maps these to specific LLM providers/models.

## Usage

**Agentfile:**
```
AGENT critic FROM agents/critic.md REQUIRES "reasoning-heavy"
AGENT helper FROM agents/helper.md REQUIRES "fast"
```

**Config profiles:**
```json
{
  "profiles": {
    "reasoning-heavy": {
      "provider": "anthropic",
      "model": "claude-opus-4-20250514"
    },
    "fast": {
      "model": "claude-haiku-20240307"
    }
  }
}
```

## Benefits

- Workflow declares *intent* (what capability is needed)
- Config controls *implementation* (which model provides it)
- Same Agentfile works in different environments
- Ops can control costs without editing workflows

**Profile inheritance:** Profiles inherit from the default `llm` config. Only specify what differs.

## Built-in LLM Roles

| Config Section | Purpose | When Used |
|----------------|---------|-----------|
| `[llm]` | Primary model | Main workflow execution, goals, sub-agents without REQUIRES |
| `[small_llm]` | Fast/cheap model | `web_fetch` summarization, security triage fallback |
| `[profiles.<name>]` | Capability-specific | Sub-agents with `REQUIRES "<name>"` |
| `[security] triage_llm` | Security triage | Tier 2 verification (points to a profile name) |

## Example Config

```toml
[llm]
model = "claude-sonnet-4-20250514"
max_tokens = 4096

[small_llm]
model = "claude-haiku-20240307"
max_tokens = 1024

[profiles.reasoning-heavy]
model = "claude-opus-4-20250514"

[profiles.fast]
model = "gpt-4o-mini"

[profiles.code-generation]
model = "claude-sonnet-4-20250514"

[security]
triage_llm = "fast"  # Use the "fast" profile for security triage
```

---

Back to [README](../../README.md) | See also: [LLM Providers](llm-providers.md), [Thinking](thinking.md)
