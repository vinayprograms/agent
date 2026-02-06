# Chapter 5: Tiered Verification

## The Cost Problem

Full LLM-based verification on every action is expensive:
- Latency: +1-2 seconds per check
- Cost: Additional API calls, token usage
- Most actions are benign and don't need verification

We use a tiered approach: fast checks filter most cases, expensive checks only when needed.

## The Verification Tiers

![Tiered Verification Pipeline](images/05-tiered-verification.png)

## Tier 1: Deterministic Checks

Fast pattern matching with no LLM calls. Runs in <1ms.

### Check 1: Untrusted Content Presence

Scan recent blocks for any with `trust=untrusted`.

If no untrusted content in recent context → **PASS**. No further checks needed.

### Check 2: Tool Risk Level

Classify tools by risk:

| High Risk | Low Risk |
|-----------|----------|
| bash | read |
| write | glob |
| web_fetch | grep |
| spawn_agent | memory_read |

Low-risk tools with untrusted context → **PASS** with logging.

High-risk tools with untrusted context → continue to pattern check.

### Check 3: Suspicious Patterns

Pattern match for injection attempts in untrusted content and tool arguments:

**Instruction Override Patterns:**
- "ignore previous/above/prior instruction"
- "new instruction/directive/task/policy"
- "forget previous/everything"

**Superseding Patterns (immutability violation):**
- "update/change/modify policy/rule/instruction"
- "override"
- "supersede"
- "disregard previous/above/prior"

**Code Execution Patterns:**
- "execute/run/call" followed by parentheses
- "curl ... | bash"

**Credential Access Patterns:**
- "api_key", "password", "token", "secret"

If any pattern matches → escalate to Tier 2.

### Check 4: Encoded Content

See [Chapter 4](04-encoded-content.md). If detected → automatic escalation.

## Tier 2: Cheap Model Triage

When deterministic checks flag an issue, we ask a fast/cheap model for triage.

**Model choice:** Haiku, Gemini Flash, GPT-4o-mini — optimized for speed, ~60x cheaper than frontier models.

**Triage question:**

The model receives the tool call details and recent untrusted content, then answers: Does this tool call appear to be influenced by instructions in the untrusted content? YES or NO.

**Response handling:**

| Response | Action |
|----------|--------|
| NO | PASS — triage cleared |
| YES | Escalate to Tier 3 |
| Ambiguous | Retry with stricter prompt |
| Still ambiguous | Escalate (fail-safe) |

**Fail-safe default:** If triage cannot determine → treat as suspicious → escalate.

## Tier 3: Full Supervisor

The existing supervision system (see [Execution Model](../execution/README.md)).

- Uses capable model (Sonnet/Opus)
- Has full decision trail context
- Returns actionable verdict: CONTINUE, REORIENT, PAUSE

Only invoked when:
- Tier 1 + Tier 2 flag an issue, OR
- Paranoid mode is enabled, OR
- SUPERVISED goal with reconciliation triggers

## Cost Analysis

| Scenario | Tier 1 | Tier 2 | Tier 3 | Total Overhead |
|----------|--------|--------|--------|----------------|
| No untrusted content | ✓ | - | - | ~0ms |
| Untrusted + low-risk tool | ✓ | - | - | ~0ms |
| Untrusted + high-risk, no flags | ✓ | - | - | ~1ms |
| Untrusted + suspicious pattern | ✓ | ✓ | - | ~100ms |
| Triage says suspicious | ✓ | ✓ | ✓ | ~2s |
| Paranoid mode | ✓ | ✓ | ✓ | ~2s per action |

**Expected distribution in typical workloads:**
- 80% pass at Tier 1
- 15% pass at Tier 2  
- 5% reach Tier 3

## Configuration

```toml
[security.verification]
triage_model = "claude-haiku"
```

| Setting | Description |
|---------|-------------|
| triage_model | Model for Tier 2 (fast/cheap) |

Tier 3 uses the supervisor model from `[supervision]` config.

Additional patterns and high-risk tools can be configured to extend the defaults.

---

Next: [Cryptographic Audit Trail](06-audit-trail.md)
