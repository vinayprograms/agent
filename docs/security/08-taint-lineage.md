# Taint Lineage Tracking

## Overview

When a security check triggers, understanding *why* can be challenging. A tool call might be flagged due to content from a web fetch that happened 50 steps ago, processed through multiple LLM responses.

Taint lineage tracking solves this by recording the dependency chain from source untrusted content to the current action.

## The Problem

Consider this workflow:

1. `web_fetch` returns content from a URL → creates block `b0001`
2. LLM processes this and responds → creates block `b0002`, tainted by `b0001`
3. `bash` is called with a command that includes content from `b0002`
4. Security check triggers on the `bash` call

Without lineage: "b0002 is untrusted" — but *why*?

With lineage: "b0002 ← b0001 (tool:web_fetch)" — traces back to the web content.

## How It Works

### Block Creation

Each content block tracks:
- `id` — Unique identifier (b0001, b0002, ...)
- `trust` — trusted, vetted, or untrusted
- `source` — Where it came from (tool:web_fetch, llm:response, etc.)
- `created_at_seq` — Session event sequence when created
- `tainted_by` — IDs of blocks that influenced this block

### Taint Propagation

**Root sources** (no parents):
- Tool results from external tools (`web_fetch`, `read`, etc.)
- User input in multi-turn conversations

**Derived blocks** (have parents):
- LLM responses after seeing untrusted content
- Tool results that process untrusted data

### Example Lineage Tree

```
● b0005 [untrusted] llm:response (seq:45)
  └─ b0003 [untrusted] tool:web_fetch (seq:30)
  └─ b0004 [untrusted] tool:read (seq:35)
```

Block `b0005` is an LLM response that was influenced by both `b0003` (web fetch) and `b0004` (file read).

## Session Log Format

The `taint_lineage` field appears in security events:

```json
{
  "type": "security_static",
  "tool": "bash",
  "meta": {
    "block_id": "b0005",
    "related_blocks": ["b0003", "b0004", "b0005"],
    "taint_lineage": [
      {
        "block_id": "b0005",
        "trust": "untrusted",
        "source": "llm:response",
        "event_seq": 45,
        "depth": 0,
        "tainted_by": [
          {
            "block_id": "b0003",
            "trust": "untrusted",
            "source": "tool:web_fetch",
            "event_seq": 30,
            "depth": 1,
            "tainted_by": []
          },
          {
            "block_id": "b0004",
            "trust": "untrusted",
            "source": "tool:read",
            "event_seq": 35,
            "depth": 1,
            "tainted_by": []
          }
        ]
      }
    ]
  }
}
```

## Replay Viewer Display

The `agent-replay` command displays taint lineage for security events:

```
   42 │ 14:30:05 │ SECURITY: static check flagged
      │          │   flags: [high_risk_tool:bash, pattern:shell_injection]
      │          │   taint lineage:
      │          │     ● b0005 [untrusted] llm:response (seq:45)
      │          │       └─ b0003 [untrusted] tool:web_fetch (seq:30)
      │          │       └─ b0004 [untrusted] tool:read (seq:35)
```

The tree shows:
- `●` — Root of the lineage (the block directly involved in the tool call)
- `└─` — Parent blocks that tainted the root
- `[trust]` — Trust level (untrusted is highlighted)
- `(seq:N)` — Event sequence for correlation with session log

## Benefits

1. **Debugging**: Quickly trace why a tool call was flagged
2. **Forensics**: Understand attack propagation paths
3. **Tuning**: Identify which sources cause false positives
4. **Compliance**: Document security decision context

## Limitations

- Lineage is only tracked within a session
- Cross-session taint (e.g., from memory) is not currently tracked
- Deep chains (>10 levels) are truncated to prevent exponential growth
