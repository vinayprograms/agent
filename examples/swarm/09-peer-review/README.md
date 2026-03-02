# 09 — Peer Review

Two agents in a review loop: coder implements, reviewer critiques. Simulates a PR review workflow.

## Agents

- **coder** — writes Go implementation
- **reviewer** — performs detailed code review with verdicts

## Usage

```bash
swarm up
swarm submit code "a concurrent-safe LRU cache with TTL expiration"
# Take the output, submit for review:
swarm submit review "$(swarm result <task_id> --output-only)"
swarm down
```

## Pattern: Bidirectional (Manual)

```
code → review → (human decides: accept or resubmit to code with feedback)
```

The human closes the loop — deciding whether to accept the review or ask the coder to revise based on feedback. This is a manual chain, not automated, because the human judgment step is the point.
