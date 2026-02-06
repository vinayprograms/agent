# Chapter 2: Reconciliation & Supervision

## Phase 3: RECONCILE

After execution, the reconcile phase performs fast, deterministic checks:

![Reconcile Phase](images/02-reconcile-phase.png)

**Key property:** No LLM calls. Pattern matching only. Runs in milliseconds.

## Reconcile Triggers

The reconciler examines the post-checkpoint and flags issues:

| Trigger | Condition | Severity |
|---------|-----------|----------|
| `concerns_raised` | Agent reported concerns in self-assessment | Medium |
| `commitment_not_met` | Agent says commitment wasn't fully met | High |
| `scope_deviation` | Scope changed during execution | High |
| `excess_assumptions` | More than 3 assumptions made | Low |

**If any flags are raised → proceed to SUPERVISE.**

**If no flags → execution continues without supervisor overhead.**

## Why Reconcile First?

The reconcile phase acts as a **cheap filter**:

| Without Reconcile | With Reconcile |
|-------------------|----------------|
| Every step → Supervisor LLM | Every step → Pattern check |
| 100% supervisor cost | ~20% supervisor cost |
| High latency always | Low latency usually |

Most steps complete without issues. Reconcile catches the ones that need attention.

## Phase 4: SUPERVISE

When reconcile flags an issue, the supervisor LLM evaluates the situation:

![Supervise Phase](images/02-supervise-phase.png)

**Supervisor receives:**
- Original goal
- Agent's commitment (pre-checkpoint)
- Execution trace (tools used, outputs)
- Agent's self-assessment (post-checkpoint)
- Reconcile flags

**Supervisor returns a verdict:** CONTINUE, REORIENT, or PAUSE.

## Supervisor Evaluation

The supervisor considers:

1. Did the agent correctly interpret the goal?
2. Did the agent stay within declared scope?
3. Was the intended outcome achieved?
4. Are the raised concerns valid?
5. Is the output appropriate for the task?

Based on this evaluation, the supervisor issues one of three verdicts (see [Chapter 5](05-supervisor-verdicts.md)).

## Checkpoint Storage

All checkpoints are persisted for audit:

![Checkpoint Structure](images/03-checkpoint-structure.png)

The checkpoint store provides:
- Save pre-checkpoint after COMMIT
- Save post-checkpoint after EXECUTE
- Save supervision record after SUPERVISE
- Query step history by ID
- Query full session history

## Example: Reconcile → Supervise Flow

**Goal:** Summarize customer data from API

**Pre-checkpoint (COMMIT):**
- Interpretation: Fetch customer records and generate summary
- Scope in bounds: Read from customer API, format summary
- Scope out of bounds: Modifying customer records, external calls
- Assumptions: API returns JSON, records have name and email fields

**Post-checkpoint (EXECUTE):**
- Commitment met: partial
- Scope changed: yes
- Concerns:
  - API returned paginated results, had to make multiple calls
  - Some records missing email field
  - Called external validation service to verify emails

**Reconcile flags:**
- `commitment_not_met`: true (partial)
- `scope_deviation`: true (called external service)
- `concerns_raised`: true

**Supervisor verdict:** REORIENT

The agent called an external validation service which was outside declared scope. Supervisor provides correction to complete the summary using only data from the customer API.

---

Next: [Supervision Modes](03-supervision-modes.md)
