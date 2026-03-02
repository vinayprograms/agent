# 10 — Summarizer Fleet

Three identical summarizers for throughput testing. Flood the swarm with summarization tasks and verify NATS queue group distribution.

## Agents

- **summarizer-1, 2, 3** — all share capability `summarize`

## Usage

```bash
swarm up
# Flood with tasks
for i in $(seq 1 10); do
  swarm submit summarize "$(cat document-$i.txt)" &
done
wait
swarm history
swarm down
```

## Pattern: Horizontal Scaling

```
         ┌─ summarizer-1
submit ──┼─ summarizer-2  (queue group: round-robin)
         └─ summarizer-3
```

Same as 04-parallel-research but tests throughput: 10 tasks across 3 agents. Verify that all three agents pick up work (not just one).
