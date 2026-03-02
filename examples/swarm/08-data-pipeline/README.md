# 08 — Data Pipeline

Classic ETL (Extract-Transform-Load) pattern with three specialized agents.

## Agents

- **extractor** — parses raw/messy data into structured JSON
- **transformer** — normalizes, deduplicates, validates, enriches
- **loader** — generates SQL statements for target schema

## Usage

```bash
swarm up
swarm chain extract "CSV with columns: name,email,signup_date,plan in mixed formats" -> transform -> load
swarm down
```

## Pattern: ETL Chain

```
extract → transform → load
```

Tests data transformation where each stage's output must conform to the next stage's expected input format. Validates structured data passing between agents.
