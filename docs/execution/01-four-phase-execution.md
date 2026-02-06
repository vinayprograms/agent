# Chapter 1: Four-Phase Execution

## The Problem

Traditional agent execution is opaque:
1. Agent receives goal
2. Agent does... something
3. Agent returns result

We have no visibility into *what* the agent intended, *how* it interpreted the task, or *whether* it stayed on track. Drift from intended behavior goes undetected.

## The Solution: Declare, Execute, Verify

Every supervised step goes through four phases:

![Four-Phase Execution](images/01-four-phases.png)

| Phase | Purpose | Output |
|-------|---------|--------|
| COMMIT | Declare intent before acting | Pre-checkpoint |
| EXECUTE | Do the work | Post-checkpoint |
| RECONCILE | Static checks for drift | Flags (if any) |
| SUPERVISE | LLM judgment | Verdict |

## Phase 1: COMMIT

Before executing, the agent must declare:

![Commit Phase](images/01-commit-phase.png)

**Commitment includes:**

| Field | Description |
|-------|-------------|
| `interpretation` | How the agent understands the task |
| `scope` | What's in bounds vs. out of bounds |
| `approach` | Strategy for completing the task |
| `predictions` | Expected outcomes |
| `assumptions` | Things the agent is assuming to be true |

**Why commit first?**

1. **Accountability** — Agent's interpretation is recorded
2. **Scope boundaries** — Clear limits before execution
3. **Drift detection** — Compare actual behavior to commitment
4. **Audit trail** — What was the agent *trying* to do?

## Phase 2: EXECUTE

The agent performs the actual work:

![Execute Phase](images/01-execute-phase.png)

**During execution:**
- Agent reasoning (chain of thought)
- Tool calls with arguments and results
- Any errors or exceptions

**After execution, agent provides self-assessment:**

| Field | Description |
|-------|-------------|
| `commitment_met` | Did execution match the commitment? (yes/no/partial) |
| `scope_changed` | Did scope expand or shift? (yes/no) |
| `concerns` | Any issues or uncertainties (list) |

The self-assessment uses categorical values, not numeric scores. LLMs reason better with qualitative judgments than precise numbers.

## Checkpoints

Both phases produce checkpoints stored for audit and supervision.

**Pre-checkpoint (COMMIT):**

| Field | Type |
|-------|------|
| step_id | string |
| timestamp | time |
| goal | string |
| interpretation | string |
| scope | in_bounds / out_bounds |
| approach | string |
| predictions | list of strings |
| assumptions | list of strings |

**Post-checkpoint (EXECUTE):**

| Field | Type |
|-------|------|
| step_id | string |
| timestamp | time |
| tools_used | list of tool calls |
| output | string |
| commitment_met | yes / no / partial |
| scope_changed | yes / no |
| concerns | list of strings |

## Unsupervised Execution

For steps without supervision, only COMMIT and EXECUTE run:

```
COMMIT → EXECUTE → (done)
```

Checkpoints are still captured for audit, but RECONCILE and SUPERVISE are skipped.

## When to Use Each Phase

| Situation | Phases Used |
|-----------|-------------|
| Trusted operation, no oversight | COMMIT + EXECUTE |
| Standard supervised step | All four phases |
| Critical step with concerns | All four + likely SUPERVISE |
| SUPERVISED HUMAN step | All four + human approval |

---

Next: [Reconciliation & Supervision](02-reconciliation-supervision.md)
