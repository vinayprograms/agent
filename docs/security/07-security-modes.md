# Chapter 7: Security Modes

## Two Modes

The system operates in one of two security modes:

```
┌─────────────────────────────────────────────────────────────┐
│                     SECURITY MODES                          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────────────┐      ┌─────────────────────┐       │
│  │      DEFAULT        │      │      PARANOID       │       │
│  ├─────────────────────┤      ├─────────────────────┤       │
│  │ Tiered verification │      │ Full verification   │       │
│  │ Smart escalation    │      │ Every action        │       │
│  │ Trust vetted blocks │      │ Re-verify vetted    │       │
│  │                     │      │                     │       │
│  │ Balanced cost/      │      │ Maximum security    │       │
│  │ security            │      │ Higher cost         │       │
│  └─────────────────────┘      └─────────────────────┘       │
│                                                             │
│  Use for: Most deployments    Use for: High-security       │
│           Internal users                Public-facing       │
│           Trusted workflows             Sensitive data      │
│                                         Compliance reqs     │
└─────────────────────────────────────────────────────────────┘
```

## Mode Comparison

| Aspect | Default | Paranoid |
|--------|---------|----------|
| Tier 1 (deterministic) | Always | Always |
| Tier 2 (triage) | When Tier 1 escalates | Always |
| Tier 3 (full supervisor) | When Tier 2 escalates | Always |
| Vetted content | Trusted | Re-verified by supervisor |
| Encoded content | Escalate to Tier 2 | Escalate to Tier 3 |
| Performance overhead | Low (5-10% of actions checked) | High (all actions checked) |
| Latency per action | ~0ms typical, ~2s worst case | ~2s for tool calls |

## Configuration

```toml
# agent.toml

[security]
# Security mode: "default" or "paranoid"
mode = "default"

# Trust level for user messages
# "trusted" - internal deployment, known users
# "untrusted" - public-facing, unknown users
user_trust = "trusted"
```

**In Agentfile:**

```
# Set security mode for this workflow
SECURITY paranoid

NAME sensitive-workflow
...
```

Agentfile setting overrides config file (more restrictive wins).

## When to Use Each Mode

### Default Mode

Choose for:
- Internal tools with trusted users
- Development and testing
- Workflows processing trusted data sources
- Cost-sensitive deployments

```toml
[security]
mode = "default"
user_trust = "trusted"
```

### Paranoid Mode

Choose for:
- Public-facing agents
- Processing sensitive/regulated data (PII, financial, health)
- Workflows with high-risk tools (bash, external APIs)
- Compliance requirements (SOC2, HIPAA, etc.)
- When user input cannot be trusted

```toml
[security]
mode = "paranoid"
user_trust = "untrusted"
```

## Invariants (Both Modes)

These rules apply regardless of mode:

1. **Untrusted content is never treated as instructions**
   - Block type enforcement is not configurable

2. **Encoded content in untrusted blocks always escalates**
   - Minimum: Tier 2 in default mode
   - Minimum: Tier 3 in paranoid mode

3. **All security decisions are signed**
   - Audit trail is always active

4. **Tool restrictions are always enforced**
   - Policy.toml allowlists/denylists apply in all modes

## Workflow-Level Override

Individual workflows can escalate (but not reduce) security:

```
# In Agentfile

# This workflow handles PII, use paranoid mode
SECURITY paranoid

NAME user-data-processor
INPUT user_id
...
```

If config says `default` but Agentfile says `paranoid`, paranoid wins.

A workflow **cannot** reduce security below the config level:
- Config: `paranoid` + Agentfile: `default` → paranoid applies
- Config: `default` + Agentfile: `paranoid` → paranoid applies

## Runtime Mode Indication

The agent logs its security mode at startup:

```
INFO  2026-02-06T19:30:00.000Z [security] mode=default user_trust=trusted
```

In paranoid mode:
```
INFO  2026-02-06T19:30:00.000Z [security] mode=paranoid user_trust=untrusted
WARN  2026-02-06T19:30:00.000Z [security] paranoid mode active - all tool calls will be verified
```

---

Next: [Testing Your Model](08-model-testing.md)
