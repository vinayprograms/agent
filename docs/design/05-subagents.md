# Chapter 5: Sub-Agents

## Two Approaches

| Approach | Declaration | When Decided |
|----------|-------------|--------------|
| Static | AGENT/USING in Agentfile | Design time |
| Dynamic | spawn_agent tool | Runtime (LLM decides) |

Both use the **same execution path** and have identical capabilities.

## Static Sub-Agents

Declare agents in Agentfile, reference in goals:

```
AGENT optimist FROM agents/optimist.md REQUIRES "reasoning-heavy"
AGENT critic FROM agents/devils-advocate.md REQUIRES "fast"

GOAL evaluate "Analyze this decision" USING optimist, critic
```

When the goal runs:
1. Spawns both agents in parallel
2. Each gets the parent's tool registry (minus spawn_agent/spawn_agents)
3. Each runs a full agentic loop with tool calls
4. Waits for both to complete
5. Synthesizes their outputs

The `FROM` path can be:
- A markdown file with a persona/prompt
- An `.agent` package for more complex sub-agents

The `REQUIRES` profile selects which LLM provider to use (defined in config).

## Dynamic Sub-Agents

The LLM can spawn sub-agents at runtime using the `spawn_agent` tool:

```
spawn_agent(role: "researcher", task: "Find facts about {topic}")
spawn_agent(role: "critic", task: "Identify biases")
```

Or spawn multiple in parallel:

```
spawn_agents(agents: [
  {role: "optimist", task: "Make the case for..."},
  {role: "pessimist", task: "Make the case against..."}
])
```

The optional `outputs` parameter enables structured output parsing.

## Unified Execution

Both static and dynamic sub-agents use the same execution path:

| Feature | Static (AGENT) | Dynamic (spawn_agent) |
|---------|---------------|----------------------|
| Tool access | Parent's registry | Parent's registry |
| MCP tools | Yes | Yes |
| Agentic loop | Yes | Yes |
| Security verification | Yes | Yes |
| Supervision phases | Yes (if enabled) | Yes (if enabled) |
| spawn_agent excluded | Yes | Yes |
| Session logging | Yes | Yes |

The only differences:
- **Static**: Persona comes from `FROM` file, model from `REQUIRES`
- **Dynamic**: Role/task specified at runtime by orchestrator LLM

## Security

All tool calls from sub-agents go through the same 3-tier security verification:

1. **Static analysis** — Policy checks, pattern matching
2. **Triage** — Fast LLM classification of suspicious calls
3. **Supervisor** — Full LLM evaluation when triggered

Sub-agents inherit the parent's security context but cannot escalate privileges.

## Depth = 1

Sub-agents cannot spawn their own sub-agents. The `spawn_agent` and `spawn_agents` tools are excluded from their tool set:

```
Orchestrator (has spawn_agent)
    ├── researcher (no spawn_agent)
    ├── critic (no spawn_agent)
    └── synthesizer (no spawn_agent)
```

This prevents infinite recursion and keeps execution predictable.

## Supervision

When supervision is enabled, sub-agents go through the same four-phase execution as main goals:

1. **COMMIT** — Sub-agent declares its intent
2. **EXECUTE** — Sub-agent does the work (with tools)
3. **RECONCILE** — Static pattern checks on the result
4. **SUPERVISE** — LLM evaluation if reconcile triggers

Sub-agents inherit supervision from their parent goal. If the parent goal is unsupervised, sub-agents are also unsupervised.

## When to Use Each

| Static (AGENT/USING) | Dynamic (spawn_agent) |
|---------------------|----------------------|
| Known agents at design time | LLM decides what's needed |
| Persona defined in files | Ad-hoc specialists |
| Explicit control | Flexible problem decomposition |
| Different model per agent | Same model as parent |

---

Next: [Packaging](06-packaging.md)
