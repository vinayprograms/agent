# 05 — Content Pipeline

Publishing workflow: draft → edit → fact-check. Tests multi-input agents and content transformation chains.

## Agents

- **writer** — drafts articles (supports topic + style inputs)
- **editor** — improves clarity, flow, conciseness
- **fact-checker** — verifies claims, flags inaccuracies

## Usage

```bash
swarm up
swarm chain write '{"topic": "why Rust is eating C++", "style": "casual explainer"}' | edit | fact-check
swarm down
```

## Pattern: Content Transformation Chain

```
write → edit → fact-check
```

Each stage transforms the content. Tests agents with multiple INPUTs and structured input passing.
