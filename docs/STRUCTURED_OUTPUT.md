# Structured Output Feature

## Overview

Structured output allows goals and agents to declare expected output fields. The executor instructs the LLM to return JSON with those fields, parses the response, and makes fields available as variables for subsequent goals.

## Syntax

### GOAL

```
GOAL <name> "<prompt>" -> output1, output2, output3
GOAL <name> "<prompt>" -> outputs USING agent1, agent2
GOAL <name> FROM path/to/prompt.md -> outputs
GOAL <name> FROM path/to/prompt.md -> outputs USING agents
```

### AGENT

```
AGENT <name> "<prompt>" -> output1, output2
AGENT <name> FROM path/to/prompt.md -> outputs
AGENT <name> FROM path/to/prompt.md -> outputs REQUIRES "profile"
```

### spawn_agent tool (dynamic)

```
spawn_agent(
  role: "researcher",
  task: "Find origins of capitalism",
  outputs: ["timeline", "key_figures", "sources"]
)
```

## Behavior

### Simple GOAL (no USING)

```
GOAL assess "Assess code at $path for $focus" -> current_state, opportunities, priority
```

Execution:
1. Build prompt from description + interpolated variables
2. Append JSON instruction:
   ```
   Respond with a JSON object containing:
   - current_state
   - opportunities  
   - priority
   ```
3. LLM returns JSON
4. Parse response, store fields as variables (`$current_state`, `$opportunities`, `$priority`)
5. Variables available to subsequent goals

### GOAL with USING (parallel agents + synthesis)

```
AGENT researcher "Research $topic" -> findings, sources
AGENT critic "Find biases in $topic" -> issues, concerns

GOAL analyze "Analyze $topic" -> summary, recommendations USING researcher, critic
```

Execution:
1. Spawn `researcher` and `critic` in parallel
2. Each agent gets JSON instruction for their declared outputs
3. Each returns structured JSON independently
4. Collect all outputs:
   ```json
   {
     "researcher": {"findings": "...", "sources": ["..."]},
     "critic": {"issues": "...", "concerns": ["..."]}
   }
   ```
5. Run implicit synthesis step:
   ```
   You have received outputs from multiple agents:

   ## researcher
   - findings: ...
   - sources: ...

   ## critic
   - issues: ...
   - concerns: ...

   Synthesize these into:
   - summary
   - recommendations

   Respond as JSON.
   ```
6. Synthesis output becomes goal's output variables

### Dynamic spawn_agent

```
spawn_agent(role: "researcher", task: "...", outputs: ["timeline", "sources"])
```

Execution:
1. Sub-agent system prompt includes JSON instruction for declared outputs
2. Sub-agent returns structured JSON
3. Orchestrator receives parsed object, not raw text
4. Without `outputs` param: freeform text response (existing behavior)

## Variable Flow

All output variables accumulate and remain available throughout workflow execution.

```
NAME example
INPUT topic

GOAL research "Research $topic" -> findings, sources
GOAL critique "Critique $findings for bias" -> issues, recommendations  
GOAL report "Write report on $topic using $sources" -> final_report

RUN pipeline USING research, critique, report
```

| After Goal | Available Variables |
|------------|---------------------|
| (start) | `$topic` |
| `research` | `$topic`, `$findings`, `$sources` |
| `critique` | `$topic`, `$findings`, `$sources`, `$issues`, `$recommendations` |
| `report` | all above + `$final_report` |

Unused variables are retained, not discarded.

## spawn_agent Tool Specification

```
spawn_agent: Spawn a sub-agent to handle a specific task.

Parameters:
  - role (required): Name/role for the sub-agent (e.g., "researcher", "critic")
  - task (required): Task description for the sub-agent
  - outputs (optional): List of field names for structured output. When provided,
    the sub-agent returns a JSON object with these fields. Use when you need
    specific data to process further. Omit for freeform text responses.

Returns:
  - If outputs specified: JSON object with declared fields
  - If outputs omitted: Plain text response

Example:
  spawn_agent(role: "researcher", task: "Find key events", outputs: ["events", "dates"])
  → {"events": [...], "dates": [...]}
```

## Validation & Warnings

| Condition | Action |
|-----------|--------|
| Output field name conflicts with INPUT | Error at parse time |
| Prompt exceeds 200 chars without FROM | Warning: consider using FROM file |
| `->` declares field not returned by LLM | Error at runtime |
| USING agents have no `->` but GOAL has `->` | Synthesis uses raw agent text |

## Backward Compatibility

- `->` is optional
- Goals/agents without `->` work exactly as before (freeform text output)
- Existing Agentfiles require no changes

## Grammar

```
GOAL  = "GOAL" name (inline-prompt | from-clause) ["->" output-list] ["USING" agent-list]
AGENT = "AGENT" name (inline-prompt | from-clause) ["->" output-list] ["REQUIRES" profile]

inline-prompt = quoted-string
from-clause   = "FROM" path
output-list   = identifier ("," identifier)*
agent-list    = identifier ("," identifier)*
```

## Implementation Files

1. `internal/agentfile/types.go` — Add `Outputs []string` to Goal and Agent structs
2. `internal/agentfile/parser.go` — Parse `->` and output list
3. `internal/executor/executor.go` — JSON instruction generation, response parsing, variable storage, synthesis step
4. `internal/tools/registry.go` — Add `outputs` param to spawn_agent tool
