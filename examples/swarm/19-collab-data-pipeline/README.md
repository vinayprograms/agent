# 19 — Collaborative Data Pipeline

Same agents as [08-data-pipeline](../08-data-pipeline/), but using `discuss` mode. ETL agents self-organize.

**Compare with:** [08-data-pipeline](../08-data-pipeline/) (explicit chain)

## Agents

- **extractor** — parses raw data into structured JSON
- **transformer** — normalizes, deduplicates, validates
- **loader** — generates SQL statements

## Usage

```bash
swarm up
swarm submit --mode discuss "take this messy CSV data with columns name, email, signup_date, plan — parse it, normalize the fields, deduplicate, and generate SQL INSERT statements for a users table"
swarm history
swarm down
```

## How It Works

This is an interesting test case. ETL has a natural ordering (extract before transform before load). In collaboration mode, agents may all try to work simultaneously — but the transformer needs extracted data and the loader needs transformed data.

The LLM triage should ideally recognize:
- Extractor: EXECUTE (raw data → structured)
- Transformer: COMMENT (needs extracted output first)
- Loader: COMMENT (needs transformed output first)

## Pattern: Inherently Sequential Collaboration

```
         ┌─ extractor   [EXECUTE — has raw data]
discuss ─┼─ transformer [COMMENT? — needs extraction first]
         └─ loader      [COMMENT? — needs transformation first]
```

This example highlights when collaboration is LESS effective than chaining. ETL is inherently sequential — agents can't self-organize around a dependency chain they can't see. Compare results with 08 to see the difference.
