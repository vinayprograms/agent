# 18 — Collaborative Content Pipeline

Same agents as [05-content-pipeline](../05-content-pipeline/), but using `discuss` mode. Writer, editor, and fact-checker self-organize.

**Compare with:** [05-content-pipeline](../05-content-pipeline/) (explicit chain)

## Agents

- **writer** — drafts articles
- **editor** — improves clarity, flow, conciseness
- **fact-checker** — verifies claims

## Usage

```bash
swarm up
swarm submit --mode discuss "write a casual explainer about why Rust is eating C++, make sure it reads well, and verify all technical claims are accurate"
swarm history
swarm down
```

## How It Works

The task description mentions writing, editing quality, and factual accuracy — all three agents should recognize their role. Interesting to see whether the fact-checker decides EXECUTE (produce a full report) or COMMENT (flag potential issues).

## Pattern: Collaborative Content

```
         ┌─ writer       [likely EXECUTE — writing task]
discuss ─┼─ editor       [EXECUTE or COMMENT — quality review]
         └─ fact-checker [EXECUTE or COMMENT — accuracy check]
```

In the chain version (05), the editor only sees the writer's draft. In collaboration, the editor and fact-checker see the original request and may produce output independently — potentially catching issues the chain version misses.
