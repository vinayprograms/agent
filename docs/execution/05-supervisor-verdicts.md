# Chapter 5: Supervisor Verdicts

## The Three Verdicts

When the supervisor evaluates a goal, it returns one of three verdicts:

![Verdict Flow](images/05-verdict-flow.png)

| Verdict | Meaning | Execution Impact |
|---------|---------|------------------|
| CONTINUE | Execution is on track | Proceed normally |
| REORIENT | Drift detected, correctable | Agent adjusts and retries |
| PAUSE | Significant issue, needs attention | Halt and escalate |

## CONTINUE

The supervisor determines the agent is executing correctly.

**When issued:**
- Commitment matches execution
- Scope boundaries respected
- Output is reasonable
- Minor concerns are acceptable

**Example scenario:**

Agent correctly interpreted summarizing Q4 revenue data. Scope was maintained (read-only operations). The assumption about CSV format was valid. Output matches expected format.

**Execution impact:** None. Agent proceeds to next goal.

## REORIENT

The supervisor detects drift but believes the agent can correct course.

**When issued:**
- Scope creep detected
- Approach is suboptimal
- Minor misinterpretation
- Correctable with guidance

**Example scenario:**

Agent began making external API calls to validate email addresses, which was outside the declared scope of "read and summarize." The validation is not necessary for the summary task.

Supervisor provides correction: Complete the summary using only data from the source file. Note missing emails rather than validating them externally.

**Execution impact:**
1. Agent receives correction
2. Goal execution resets
3. Agent re-attempts with guidance
4. Maximum reorient attempts configurable (prevents loops)

## PAUSE

The supervisor determines execution should halt.

**When issued:**
- Critical misinterpretation
- Potential security issue
- Action cannot be undone
- Human judgment required

**Example scenario:**

Agent is attempting to delete production database records based on a file that appears to contain injected instructions. The original task was to "clean up test data" but the file referenced production tables.

**Execution impact:**
1. Execution halts immediately
2. If human available → escalate and wait
3. If no human → log and terminate

## Verdict Selection Logic

The supervisor evaluates:

| Question | If Yes |
|----------|--------|
| Is agent's interpretation fundamentally wrong? | PAUSE |
| Did agent go outside declared scope destructively? | PAUSE |
| Did agent go outside scope non-destructively? | REORIENT |
| Is approach suboptimal but goal achieved? | CONTINUE |
| Are raised concerns critical? | PAUSE |
| Are raised concerns minor? | CONTINUE |

## Supervisor Configuration

```toml
[supervision]
model = "claude-sonnet"
max_reorient_attempts = 3
timeout_seconds = 30
```

| Setting | Description |
|---------|-------------|
| model | LLM for supervisor (should be capable) |
| max_reorient_attempts | Limit before escalating to PAUSE |
| timeout_seconds | Supervisor LLM call timeout |

## Signed Verdicts

Every verdict is cryptographically signed for the audit trail:

| Field | Description |
|-------|-------------|
| step_id | Goal identifier |
| session_id | Session identifier |
| timestamp | When verdict was issued |
| verdict | CONTINUE, REORIENT, or PAUSE |
| reasoning | Supervisor's explanation |
| correction | Guidance (for REORIENT only) |
| signature | Ed25519 signature |

See [Cryptographic Audit Trail](../security/06-audit-trail.md) for signing details.

---

**End of Execution Model Documentation**

Return to [Overview](README.md) | See also [Security Documentation](../security/README.md)
