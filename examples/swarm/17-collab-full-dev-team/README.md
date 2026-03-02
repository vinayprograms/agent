# 17 — Collaborative Full Dev Team

Same agents as [03-full-dev-team](../03-full-dev-team/), but using `discuss` mode. Three agents self-organize to implement, test, and document.

**Compare with:** [03-full-dev-team](../03-full-dev-team/) (explicit chain)

## Agents

- **coder** — writes Go implementation
- **tester** — writes comprehensive tests
- **documenter** — writes developer documentation

## Usage

```bash
swarm up
swarm submit --mode discuss "build an HTTP rate limiter using the token bucket algorithm in Go — implement it, write thorough tests, and create developer documentation"
swarm history
swarm down
```

## How It Works

All three agents see the task. Each decides independently whether to EXECUTE, COMMENT, or SKIP based on their resume match. All three should EXECUTE — the task explicitly mentions implementation, testing, and documentation.

## Pattern: Collaborative (All Contribute)

```
         ┌─ coder      [likely EXECUTE]
discuss ─┼─ tester     [likely EXECUTE]
         └─ documenter [likely EXECUTE]
```

Unlike the chain (03), there's no guaranteed ordering. All three may work in parallel, or one may COMMENT while others EXECUTE.
