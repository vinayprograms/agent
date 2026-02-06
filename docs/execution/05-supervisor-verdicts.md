# Chapter 5: Supervisor Verdicts

## The Three Verdicts

When the supervisor evaluates a step, it returns one of three verdicts:

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

**Example:**

```
VERDICT: CONTINUE
REASONING: Agent correctly interpreted the task as summarizing Q4 
revenue data. Scope was maintained (read-only operations). The 
assumption about CSV format was valid. Output matches expected 
format and content.
```

**Execution impact:** None. Agent proceeds to next step.

## REORIENT

The supervisor detects drift but believes the agent can correct course.

**When issued:**
- Scope creep detected
- Approach is suboptimal
- Minor misinterpretation
- Correctable with guidance

**Example:**

```
VERDICT: REORIENT
REASONING: Agent began making external API calls to validate email 
addresses, which was outside the declared scope of "read and summarize."
The validation is not necessary for the summary task.
CORRECTION: Complete the summary using only data from the source file. 
Note any missing or malformed email addresses in the summary rather 
than attempting external validation.
```

**Execution impact:**
1. Agent receives correction
2. Step execution resets
3. Agent re-attempts with guidance
4. Correction included in context

```go
func (e *Executor) reorientAndRetry(step Step, correction string) error {
    // Add correction to agent context
    e.addSystemMessage(fmt.Sprintf(
        "SUPERVISOR CORRECTION: %s\n\nPlease re-attempt the task with this guidance.",
        correction,
    ))
    
    // Re-execute step (with attempt counter to prevent loops)
    e.attempts[step.ID]++
    if e.attempts[step.ID] > e.maxAttempts {
        return ErrMaxAttemptsExceeded
    }
    
    return e.executeStep(step)
}
```

## PAUSE

The supervisor determines execution should halt.

**When issued:**
- Critical misinterpretation
- Potential security issue
- Action cannot be undone
- Human judgment required

**Example:**

```
VERDICT: PAUSE
REASONING: Agent is attempting to delete production database records 
based on a file that appears to contain injected instructions. The 
original task was to "clean up test data" but the file referenced 
production tables. This requires human verification before proceeding.
```

**Execution impact:**
1. Execution halts immediately
2. If human available → escalate and wait
3. If no human → log and terminate

```go
func (e *Executor) pauseForHuman(step Step, verdict Verdict) error {
    // Log the pause
    e.log.Warn("execution paused",
        "step", step.ID,
        "reason", verdict.Reasoning,
    )
    
    if e.hasHumanConnection() {
        // Escalate to human
        response, err := e.human.RequestDecision(HumanRequest{
            StepID:    step.ID,
            Reasoning: verdict.Reasoning,
            Options:   []string{"proceed", "abort", "modify"},
        })
        if err != nil {
            return err
        }
        
        switch response.Decision {
        case "proceed":
            return e.executeStep(step)  // Human approves
        case "abort":
            return ErrAbortedByHuman
        case "modify":
            return e.reorientAndRetry(step, response.Correction)
        }
    }
    
    // No human available
    return ErrPausedNoHuman
}
```

## Verdict Selection Logic

The supervisor uses these criteria:

```
IF agent's interpretation fundamentally wrong:
    → PAUSE (needs human to clarify intent)

ELSE IF agent went outside declared scope:
    IF action was destructive/irreversible:
        → PAUSE
    ELSE:
        → REORIENT with scope reminder

ELSE IF agent's approach is suboptimal but goal achieved:
    → CONTINUE (don't over-correct)

ELSE IF agent raised concerns that seem valid:
    IF concerns are critical:
        → PAUSE for human review
    ELSE:
        → CONTINUE (agent was appropriately cautious)

ELSE IF confidence is low but output looks correct:
    → CONTINUE (agent may be overly cautious)

ELSE:
    → CONTINUE
```

## Supervisor Configuration

```toml
[supervision]
# Model for supervisor (should be capable)
model = "claude-sonnet"

# Maximum reorient attempts before escalating to PAUSE
max_reorient_attempts = 3

# Timeout for supervisor LLM call
timeout_seconds = 30

# Whether to always run supervisor (vs. only on reconcile flags)
always_supervise = false
```

## Signed Verdicts

Every verdict is cryptographically signed for the audit trail:

```go
type SupervisionRecord struct {
    StepID     string    `json:"step_id"`
    SessionID  string    `json:"session_id"`
    Timestamp  time.Time `json:"timestamp"`
    
    Verdict    string    `json:"verdict"`     // CONTINUE | REORIENT | PAUSE
    Reasoning  string    `json:"reasoning"`
    Correction string    `json:"correction"`  // Only for REORIENT
    
    Signature  string    `json:"signature"`   // Ed25519
}
```

See [Cryptographic Audit Trail](../security/06-audit-trail.md) for signing details.

---

**End of Execution Model Documentation**

Return to [Overview](README.md) | See also [Security Documentation](../security/README.md)
