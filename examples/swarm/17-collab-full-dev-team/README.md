# 17 — Managed Full Dev Team

An orchestrator agent decomposes tasks into features and assigns them to worker replicas. Workers execute in isolation — no peer awareness, no scope drift.

**Compare with:** [03-full-dev-team](../03-full-dev-team/) (explicit chain, capability-split agents)

## Agents

- **orchestrator** (manager) — decomposes tasks into features, assigns to workers, monitors progress via `discuss.*`, sends corrections via `work.<instance-id>.*`
- **developer** ×3 (worker replicas) — builds features end-to-end (code, tests, docs). Each replica handles one feature in complete isolation.

## Usage

```bash
swarm up
swarm submit develop "build an HTTP rate limiter using the token bucket algorithm in Go — implement it, write thorough tests, and create developer documentation"
swarm history
swarm down
```

## How It Works

1. The orchestrator receives the task and decomposes it into independent features.
2. Each feature is published to `work.develop.*` where NATS queue groups deliver one feature per worker replica.
3. Workers execute in isolation — they see only their assigned feature, not each other's work.
4. Workers post progress updates to `discuss.*` (write-only — they never read it).
5. The orchestrator monitors `discuss.*` and sends corrective guidance via `work.<instance-id>.*` if a worker drifts.
6. Workers complete and post results to `done.develop.*`.

## Pattern: Managed Parallel (Isolated Execution)

```
                   ┌─ developer-a1b2  [feature 1]
task → orchestrator┼─ developer-c3d4  [feature 2]
                   └─ developer-e5f6  [feature 3]
```

Unlike the old collaborative model (where agents self-organized via CLAIMs on `discuss.*`), workers here never see each other's output. The orchestrator handles all coordination. This prevents the "Ultron problem" where every agent builds the entire system regardless of agreed scope.
