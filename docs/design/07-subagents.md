# Chapter 7: Sub-Agents

## Overview

The agent can spawn sub-agents for parallel task execution. This enables the orchestrator pattern — a main agent coordinating multiple workers.

## Constraints

| Property | Value | Rationale |
|----------|-------|-----------|
| Maximum depth | 1 | Sub-agents cannot spawn sub-agents |
| State sharing | None | Complete isolation between parent and child |
| Context sharing | None | Each sub-agent has fresh context |
| Permission escalation | Prohibited | Sub-agent cannot exceed parent's permissions |

## Spawning

Two tools for spawning:

| Tool | Use Case |
|------|----------|
| spawn_agent | Single sub-agent |
| spawn_agents | Multiple sub-agents in parallel |

### spawn_agent

Spawns a single sub-agent and waits for result.

| Parameter | Description |
|-----------|-------------|
| task | What the sub-agent should do |
| agent | Optional: agent definition to use |

### spawn_agents

Spawns multiple sub-agents in parallel, collects all results.

| Parameter | Description |
|-----------|-------------|
| tasks | Array of task definitions |

Tasks run concurrently. Results returned in original order.

## Isolation Model

Each sub-agent runs in complete isolation:

| Aspect | Parent | Sub-Agent |
|--------|--------|-----------|
| Context | Full workflow context | Only task description |
| Memory | Has access | No access |
| Session | Main session | Separate session |
| Tools | Full registry | Inherited registry |
| Policy | Parent's policy | Same or more restrictive |

**Nothing is shared.** Sub-agent cannot read parent's memory, context, or intermediate state.

## Communication

| Direction | Mechanism |
|-----------|-----------|
| Parent → Child | Task description only |
| Child → Parent | Result string only |

No side channels. Parent cannot observe child's intermediate steps. Child cannot signal parent during execution.

## Example

Orchestrator spawning parallel analyzers:

```
NAME code-review-orchestrator
INPUT repo_path

GOAL analyze "Analyze {repo_path} using parallel reviewers"
```

The agent for this goal might use spawn_agents to:
1. Spawn security reviewer
2. Spawn performance reviewer
3. Spawn style reviewer
4. Collect all results
5. Synthesize final report

Each reviewer runs in parallel with isolated context.

## Supervision

Sub-agents inherit the parent's supervision mode unless overridden.

| Parent Mode | Sub-Agent Mode |
|-------------|----------------|
| UNSUPERVISED | UNSUPERVISED |
| SUPERVISED | SUPERVISED |
| SUPERVISED HUMAN | SUPERVISED HUMAN |

Sub-agent supervision operates independently — the parent doesn't receive sub-agent's supervision verdicts.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Sub-agent fails | Error returned to parent |
| One of N fails | Other results still collected |
| Parent timeout | Sub-agents terminated |
| Sub-agent hangs | Parent's timeout applies |

## Why No Nesting?

Allowing sub-agents to spawn sub-agents creates:
- Unbounded resource usage
- Complex failure modes
- Difficult debugging
- Unclear supervision chains

Depth = 1 keeps the model simple and predictable. If deeper orchestration is needed, chain workflows instead.

---

**End of Design Documentation**

Return to [Overview](README.md) | See also [Execution Model](../execution/README.md) | [Security](../security/README.md)
