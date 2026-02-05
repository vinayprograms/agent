# Supervised Workflow Examples

This directory contains examples demonstrating the supervision system for drift detection and course correction.

## Files

### `basic-supervised.agent`
Basic example showing global supervision. All steps are automatically supervised.

### `mixed-supervision.agent`
Shows mixing supervised and unsupervised steps in the same workflow.

### `human-required.agent`
Example requiring human approval for critical steps.

### `deployment-pipeline.agent`
Realistic deployment pipeline with progressive supervision levels.

## Supervision Modes

### Autonomous Mode (`SUPERVISED`)
```
SUPERVISED
NAME my-workflow
GOAL analyze "Analyze data"
```
- Supervisor monitors execution
- Auto-corrects when drift detected
- Falls back to autonomous decisions if no human available

### Human-Required Mode (`SUPERVISED HUMAN`)
```
GOAL deploy "Deploy to production" SUPERVISED HUMAN
```
- Supervisor monitors execution
- Requires human approval for corrections
- Hard fails if no human available at runtime

### Opt-Out (`UNSUPERVISED`)
```
SUPERVISED
NAME my-workflow
GOAL trivial "Quick task" UNSUPERVISED
```
- Skips supervision for trusted/trivial steps
- Reduces latency for known-safe operations

## Four-Phase Execution

When supervision is enabled, each step goes through:

1. **COMMIT** - Agent declares intent before execution
2. **EXECUTE** - Agent performs the work
3. **RECONCILE** - Static pattern checks (fast, no LLM)
4. **SUPERVISE** - LLM evaluation if triggered (only when needed)

## Pre-flight Check

Before execution begins, the system checks:
- If any step requires `SUPERVISED HUMAN`
- If a human connection (ACP/A2A) is available
- Hard fails if human required but unavailable

This prevents wasted execution on workflows that would fail mid-run.
