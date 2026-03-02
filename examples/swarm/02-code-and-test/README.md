# 02 — Code & Test

Two-agent linear pipeline. Coder writes Go code, tester writes tests for it.

## Agents

- **coder** — writes Go implementation
- **tester** — writes Go tests

## Usage

```bash
swarm up
swarm submit code "a stack data structure with push, pop, and peek"
# Take the output and feed to tester:
swarm submit test "tests for a stack with push, pop, peek operations"
# Or use chain:
swarm chain code "a stack data structure" -> test
swarm down
```

## Pattern: Linear Chain

```
code → test
```

Tests the simplest multi-agent pattern: output of one feeds into the next.
