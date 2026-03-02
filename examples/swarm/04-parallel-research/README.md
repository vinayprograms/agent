# 04 — Parallel Research

Three identical research agents sharing the same capability. Tasks are load-balanced via NATS queue groups — each task goes to exactly one agent.

## Agents

- **researcher-1, 2, 3** — all share capability `research`

## Usage

```bash
swarm up
# Submit multiple tasks — they'll be distributed across the 3 researchers
swarm submit research "zero-knowledge proofs"
swarm submit research "CRISPR gene editing 2026"
swarm submit research "neuromorphic computing"
swarm history
swarm down
```

## Pattern: Fan-Out (Load Balanced)

```
         ┌─ researcher-1
submit ──┼─ researcher-2  (NATS queue group distributes)
         └─ researcher-3
```

Key test: three tasks submitted rapidly should be handled by different agents (not all by one).
