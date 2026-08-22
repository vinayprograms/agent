# Chapter 6: Security Events

## Purpose

Even with all defenses, attacks may succeed. A record of every security
decision enables:

- **Detection**: Identify that an attack occurred
- **Forensics**: Understand what happened and how
- **Compliance**: Demonstrate security controls were active

## The Session Log Is the Trail

Every security decision is written to the session JSONL log
(`agent --session` / `agent replay`). There is no separate, signed audit
store: an earlier design generated a per-session Ed25519 keypair and signed
each decision, but that record was never exported or verifiable from the
CLI, and it was removed in the agentkit v1.2.0 migration. The session log is
the single source of truth.

## Event Types

| Event | When |
|-------|------|
| `security_block` | Untrusted content registered (tool result, MCP response) |
| `security_static` | Tier 1 deterministic check ran (patterns, entropy, skip list) |
| `security_triage` | Tier 2 screener LLM call |
| `security_supervisor` | Tier 3 reviewer LLM call |
| `security_decision` | Final verdict for the tool call |

Each event carries the tool name, the block ids involved, the flags raised
(`tool:<name>`, pattern names, `encoded_content`), the verdict, latency, and
for LLM tiers the token counts. `security_static` also carries the taint
lineage (see [Chapter 8](08-taint-lineage.md)). Bash commands additionally
log shellguard decisions (deterministic and LLM steps) as they pass through
the gate.

## What The Trail Shows

| Observation | Meaning |
|-------------|---------|
| `security_static` with no flags | No untrusted content was relevant, or tool is on the skip list |
| `security_decision` verdict `allow` after `security_supervisor` | Reviewer judged the action safe |
| `security_decision` verdict `deny` | Action blocked; reason recorded |
| Tool call with no `security_decision` | Security system was bypassed (red flag!) |

## Integrity

The session log is an append-only file written by the agent process. It is
**not** cryptographically signed: an attacker with write access to the log
directory could alter it. For compliance-critical deployments, treat the log
like any other audit artifact:

- Ship session logs to immutable storage (S3 with Object Lock, WORM storage)
- Include logs in regular backup verification
- Alert on tool calls that lack a matching `security_decision` event

## Replay

`agent replay <session>` reconstructs the run, including every security
event, so a reviewer can follow the chain from the untrusted block through the
tiers to the final decision.

---

Next: [Security Modes](07-security-modes.md)
