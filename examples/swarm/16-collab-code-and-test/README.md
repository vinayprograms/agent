# 16 — Collaborative Code & Test

Same agents as [02-code-and-test](../02-code-and-test/), but using `discuss` mode instead of chaining. Agents self-organize via embedding similarity and LLM triage.

**Compare with:** [02-code-and-test](../02-code-and-test/) (explicit chain)

## Agents

- **coder** — writes Go implementation
- **tester** — writes Go tests

## Usage

```bash
swarm up
# Broadcast to all agents via discuss mode
swarm submit --mode discuss "implement a stack data structure with push, pop, and peek in Go, then write tests for it"
swarm history
swarm down
```

## How It Works

1. Task is published to `discuss.<task_id>` — all agents see it
2. **Round 1 (Embedding):** Each agent checks semantic similarity to its resume
3. **Round 2 (LLM Triage):** Relevant agents decide: EXECUTE, COMMENT, or SKIP
4. Coder likely decides EXECUTE (implementation task matches)
5. Tester may EXECUTE (testing matches) or COMMENT (add testing suggestions)

## Pattern: Collaborative (Self-Organizing)

```
         ┌─ coder   [EXECUTE? COMMENT? SKIP?]
discuss ─┤
         └─ tester  [EXECUTE? COMMENT? SKIP?]
```

## Chain vs Collaboration

| Aspect | Chain (02) | Collaboration (16) |
|--------|------------|-------------------|
| Sequencing | Human-defined | Agent-decided |
| Task description | Per-capability | Single holistic task |
| Predictability | High | Variable |
| Creativity | Constrained | Emergent |
