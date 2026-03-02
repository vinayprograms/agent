# 23 — Collaborative API Design

Same agents as [14-api-design-workshop](../14-api-design-workshop/), but using `discuss` mode. Designer, implementer, and tester self-organize.

**Compare with:** [14-api-design-workshop](../14-api-design-workshop/) (explicit chain)

## Agents

- **api-designer** — creates OpenAPI 3.0 specs
- **implementer** — builds Go server from spec
- **api-tester** — writes integration tests

## Usage

```bash
swarm up
swarm submit --mode discuss "design, implement, and test a bookmarks API — should support CRUD for bookmarks with tags, search by tag, and bulk import/export"
swarm history
swarm down
```

## How It Works

All three agents should EXECUTE — the task mentions design, implementation, and testing. But without sequential coordination:
- The implementer designs its own API instead of following the designer's spec
- The tester tests against its own assumptions, not the actual implementation
- Each agent produces valid output, but they're not aligned

## Pattern: Spec-Driven vs Independent

```
         ┌─ api-designer [EXECUTE — creates spec]
discuss ─┼─ implementer  [EXECUTE — implements... what spec?]
         └─ api-tester   [EXECUTE — tests... what implementation?]
```

This is the **strongest argument for chaining over collaboration**. API design is inherently contract-driven — the spec IS the coordination mechanism. Without it flowing through the chain, each agent reinvents the wheel.

Compare the outputs: chain (14) produces a coherent spec → implementation → test suite. Collaboration (23) produces three independent interpretations of "bookmarks API."
