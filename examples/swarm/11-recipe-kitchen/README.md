# 11 — Recipe Kitchen

Three specialized agents forming a complete recipe-to-table pipeline. A fun, non-technical example.

## Agents

- **chef** — creates detailed recipes
- **nutritionist** — analyzes nutritional content, dietary flags
- **plater** — designs restaurant-quality plating guides

## Usage

```bash
swarm up
swarm chain cook "a modern take on eggs benedict for brunch" -> analyze-nutrition -> plate
swarm down
```

## Pattern: Domain Specialization Chain

```
cook → analyze-nutrition → plate
```

Tests non-code workflows. Each agent is a domain specialist with very different output formats (recipe → data table → visual description).
