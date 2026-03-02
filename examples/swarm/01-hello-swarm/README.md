# 01 — Hello Swarm

Minimal swarm with a single agent. Verifies basic setup: NATS connection, task submission, result retrieval.

## Agents

- **greeter** — says hello and shares a fact about a topic

## Usage

```bash
swarm up
swarm submit greet "quantum computing"
swarm down
```

## Expected Flow

1. Greeter receives task on `work.greet.<task_id>`
2. Generates greeting with fact
3. Publishes result on `done.greet.<task_id>`
