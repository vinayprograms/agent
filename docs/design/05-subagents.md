# Chapter 5: Sub-Agents

## Two Approaches

| Approach | Declaration | When Decided |
|----------|-------------|--------------|
| Static | AGENT/USING in Agentfile | Design time |
| Dynamic | spawn_agent tool | Runtime (LLM decides) |

Both can be used in the same workflow.

## Static Sub-Agents

Declare agents in Agentfile, reference in goals:

```
AGENT security FROM security-scanner.agent REQUIRES "reasoning-heavy"
AGENT style FROM style-checker.agent REQUIRES "fast"

GOAL audit "Review $code_path" USING security, style
```

When the goal runs:
1. Spawns security-scanner.agent with code_path as input
2. Spawns style-checker.agent in parallel
3. Waits for both to complete
4. Synthesizes their outputs

## Dynamic Sub-Agents

The LLM can spawn sub-agents at runtime using the `spawn_agent` tool:

```
spawn_agent(role: "researcher", task: "Find facts about {topic}")
spawn_agent(role: "critic", task: "Identify biases")
```

The optional `outputs` parameter enables structured output.

Execution trace:
```
▶ Starting goal: research
  → Tool: spawn_agent
  ⊕ Spawning sub-agent: researcher
    → Tool: web_search
    → Tool: web_fetch
  ⊖ Sub-agent complete: researcher
  → Tool: spawn_agent
  ⊕ Spawning sub-agent: critic
  ⊖ Sub-agent complete: critic
✓ Completed goal: research
```

## Isolation Model

Each sub-agent runs in complete isolation:

| Aspect | Description |
|--------|-------------|
| Own tools | Only tools declared in sub-agent's package |
| Own memory | Fresh context, no shared state |
| Own execution loop | Runs to completion independently |
| Own context window | Separate from orchestrator |

Nothing is shared. Parent passes input, child returns output.

## Depth = 1

Sub-agents cannot spawn their own sub-agents. The `spawn_agent` tool is excluded from their tool set:

```
Orchestrator (has spawn_agent)
    ├── researcher (no spawn_agent)
    ├── critic (no spawn_agent)
    └── synthesizer (no spawn_agent)
```

This prevents infinite recursion and keeps execution simple.

## When to Use Each

| Static (AGENT/USING) | Dynamic (spawn_agent) |
|---------------------|----------------------|
| Known agents at design time | LLM decides what's needed |
| Packaged agents with policies | Ad-hoc specialists |
| Explicit control | Flexible problem decomposition |

---

Next: [Packaging](06-packaging.md)
