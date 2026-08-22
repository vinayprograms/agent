# Goal Limits

`[limits]` bounds what a **single goal** may consume before the executor stops
it. Without a limit an agentic goal can loop indefinitely — one shipped example
ran for ten minutes and 66 tool calls with nothing to show for it.

## Configuration

```toml
[limits]
max_tool_calls = 40    # tool calls per goal
max_turns = 25         # LLM turns per goal
max_duration = "10m"   # wall-clock per goal
```

Every field is optional and **zero means unlimited**, which is the default: a
config without a `[limits]` table behaves exactly as before.

`max_duration` takes a Go duration string (`"90s"`, `"10m"`, `"1h30m"`). A value
that does not parse is treated as unlimited rather than as a shorter limit, so a
typo cannot silently cut a run short.

## Scope

A budget covers a whole goal: every convergence iteration of a `CONVERGE` goal
and every sub-agent it spawns draw on the same allowance. Each goal in a
workflow starts with a fresh one.

## What happens when a goal runs out

The goal **ends**; the run does not. The executor:

1. returns whatever the goal produced so far, so its `-> outputs` still bind,
2. logs a warning and records a session event reading
   `goal "research" exceeded budget: 40 tool calls (max 40)`,
3. continues with the next goal in the `RUN` step.

Supervision still reconciles the partial output, so a supervised goal that runs
out of budget is reviewed like any other.

## Background work after Ctrl-C

Separately from `[limits]`, work the executor detaches from the run's own
context — observation extraction and async tools started right before the run
is cancelled — gets its own 2-minute deadline (`context.WithTimeout` over a
`context.WithoutCancel`'d parent) rather than running unbounded. This keeps a
Ctrl-C from hanging on an LLM/tool call that no longer has a caller waiting on
it; it is not configurable via `[limits]`.
