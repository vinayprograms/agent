# Chapter 6: Persistence

## Overview

The agent persists data in three stores:

| Store | Format | Purpose |
|-------|--------|---------|
| Sessions | SQLite | Execution state, events, history |
| Memory | JSON file | Agent key-value memory |
| Checkpoints | JSON files | Supervision audit trail |

## Sessions (SQLite)

Sessions track workflow execution from start to finish.

### Schema

**sessions table:**

| Column | Type | Description |
|--------|------|-------------|
| id | TEXT | Unique session identifier |
| workflow_name | TEXT | Name from Agentfile |
| inputs | TEXT | JSON-encoded inputs |
| state | TEXT | JSON-encoded execution state |
| outputs | TEXT | JSON-encoded outputs |
| status | TEXT | running, completed, failed |
| result | TEXT | Final result |
| error | TEXT | Error message (if failed) |
| created_at | DATETIME | Session start |
| updated_at | DATETIME | Last update |

**events table:**

| Column | Type | Description |
|--------|------|-------------|
| id | INTEGER | Auto-increment |
| session_id | TEXT | Foreign key to sessions |
| type | TEXT | Event type |
| goal | TEXT | Current goal |
| content | TEXT | Event content |
| tool | TEXT | Tool name (if tool call) |
| args | TEXT | JSON-encoded arguments |
| error | TEXT | Error (if any) |
| duration_ms | INTEGER | Duration in milliseconds |
| timestamp | DATETIME | When event occurred |

### Location

Default: `~/.agent/sessions.db`

Configurable:
```toml
[session]
database = "/path/to/sessions.db"
```

## Memory (JSON)

Agent memory provides key-value storage across sessions. Used by memory_* tools.

### Format

```json
{
  "user.name": "Vinay",
  "user.preferences.timezone": "America/New_York",
  "project.current": "headless-agent"
}
```

### Location

Default: `~/.agent/memory.json`

Configurable:
```toml
[memory]
path = "/path/to/memory.json"
```

### Operations

| Tool | Operation |
|------|-----------|
| memory_read | Get value by key |
| memory_write | Set value by key |
| memory_list | List keys (with prefix filter) |
| memory_search | Search values |

## Checkpoints

Checkpoints capture execution state at each phase for supervision and audit.

### Structure

Each supervised goal produces:

| Checkpoint | Content |
|------------|---------|
| Pre-checkpoint | Commitment, scope, approach, predictions, assumptions |
| Post-checkpoint | Tools used, output, self-assessment |
| Supervision record | Verdict, reasoning, signature (if supervised) |

### Location

Default: `~/.agent/checkpoints/<session-id>/`

Each goal creates files:
- `<goal-id>.pre.json`
- `<goal-id>.post.json`
- `<goal-id>.supervision.json` (if supervised)

### Retention

Checkpoints are retained for audit. Configure cleanup:

```toml
[checkpoints]
path = "/path/to/checkpoints"
retention_days = 90
```

## Lifecycle

| Phase | Session | Memory | Checkpoints |
|-------|---------|--------|-------------|
| Start | Created | Loaded | Directory created |
| Execution | Updated per event | Updated on memory_write | Written per phase |
| End | Marked complete/failed | Persisted | Retained |

---

Next: [Sub-Agents](07-subagents.md)
