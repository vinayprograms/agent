# Dynamic Sub-Agent Spawning

The agent has access to `spawn_agent` tool by default. When the LLM determines a task would benefit from delegation, it spawns sub-agents automatically.

## Agentfile

```
NAME dynamic-research

INPUT topic

GOAL research
  Research {{topic}} thoroughly using multiple perspectives
```

## Usage

```bash
agent run -f Agentfile --config agent.json --input topic="quantum computing"
```

## What happens

The LLM receives the `spawn_agent` tool and orchestrator guidance in its system prompt. It can dynamically spawn sub-agents when appropriate:

- `spawn_agent(role: "researcher", task: "Find recent advances in quantum computing")`
- `spawn_agent(role: "critic", task: "Identify limitations and challenges")`
- `spawn_agent(role: "synthesizer", task: "Combine findings into a summary")`

## CLI Output

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
  → Tool: write
✓ Completed goal: research
```

## Sub-agent isolation

- Sub-agents run with their own LLM context (no shared history)
- Sub-agents have access to all tools EXCEPT `spawn_agent` (depth=1)
- Sub-agent output is returned to the orchestrator for synthesis
