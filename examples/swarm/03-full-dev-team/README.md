# 03 — Full Dev Team

Three-agent pipeline mimicking a dev workflow: implement, test, document.

## Agents

- **coder** — writes Go implementation
- **tester** — writes comprehensive tests
- **documenter** — writes developer documentation

## Usage

```bash
swarm up
swarm chain code "an HTTP rate limiter with token bucket algorithm" | test | document
swarm down
```

## Pattern: Three-Stage Linear Chain

```
code → test → document
```

Each agent receives the task description (not the previous agent's output directly).
For chaining where output feeds input, use `swarm chain`.
