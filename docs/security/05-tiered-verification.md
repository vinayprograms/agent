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

### Check 2: Tool Skip List

The guard is default-check: every tool call made while untrusted content
exists is verified unless the tool is on the configured **skip list**. The
agent builds the skip list from the registered tools, minus the tools that can
act on the outside world or destroy data:

| Always checked (never skipped) | Skipped (read-only / local) |
|-------------------------------|-----------------------------|
| bash | read, glob, grep, ls, head, tail, tree, diff |
| write, edit, patch | cp, mkdir, pwd, hostname, whoami, sysinfo, datetime, env, which, git |
| rm, mv | web_search |
| web_fetch | scratchpad_*, remember, recall |
| spawn_agents | |

Skipped tools with untrusted context → **PASS** with logging.

Checked tools with untrusted context → continue to pattern check. MCP tools
are never on the skip list, so they are always checked.

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

If any pattern matches → escalate to Tier 2. The flags recorded in the
session event are spelled `tool:<name>` for the tool-level trigger and
`<pattern_name>` for each matching pattern.

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
| Untrusted + skipped tool | ✓ | - | - | ~0ms |
| Untrusted + checked tool, no flags | ✓ | - | - | ~1ms |
| Untrusted + suspicious pattern | ✓ | ✓ | - | ~100ms |
| Triage says suspicious | ✓ | ✓ | ✓ | ~2s |
| Paranoid mode | ✓ | ✓ | ✓ | ~2s per action |

**Expected distribution in typical workloads:**
- 80% pass at Tier 1
- 15% pass at Tier 2  
- 5% reach Tier 3

## No Approval Caching

Each `VerifyToolCall` runs the full pipeline fresh. There is no caching of previous verification results.

**Why no caching?** The same untrusted block can be benign for one operation but dangerous for another:

| Tool Call | Block Content | Verdict |
|-----------|---------------|---------|
| `web_fetch(url from block)` | URL to documentation site | ALLOW |
| `bash(command from block)` | Same block, different usage | DENY |

Caching "block X is safe" would miss context-dependent attacks where benign-looking content is repurposed for malicious operations.

**Implication:** A single untrusted block may be checked many times during execution. In a typical agent run:

- Block registered from `web_fetch` result
- Checked again when content used in `write` call
- Checked again when content influences `spawn_agents` task
- Checked again for each subsequent non-skipped tool call

For an agent run with 50 tool calls where 30 are non-skipped after untrusted content exists, expect ~30 triage calls. At ~100ms and ~$0.0001 per triage call:

| Metric | Value |
|--------|-------|
| Latency overhead | ~3 seconds total |
| Cost overhead | ~$0.003 |

This is acceptable for the security guarantee it provides. The design prioritizes safety over micro-optimization.

## Configuration

```toml
[security.verification]
triage_model = "claude-haiku"
```

| Setting | Description |
|---------|-------------|
| triage_model | Model for Tier 2 (fast/cheap) |

Tier 3 uses the supervisor model from `[supervision]` config.

Additional patterns and keywords are configured in `policy.toml` under
`[content.security]` (`patterns`, `keywords`). The skip list is fixed by the
agent (see Check 2) and is not configurable.

---

Next: [Security Events](06-audit-trail.md)
