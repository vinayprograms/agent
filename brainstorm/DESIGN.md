# Headless Agent — Requirements Specification

## Overview

A single-process Go agent that executes LLM-driven workflows defined in a declarative DSL (Agentfile). Designed to run headless (no UI), deployable as a binary or Docker container, and built for eventual integration into a distributed agent swarm.

---

## Goals

1. **Headless**: No UI dependency, stdio-based, scriptable
2. **Declarative workflows**: Agentfile DSL defines goals, agent figures out execution
3. **Secure by default**: Allowlist-based tool permissions
4. **Observable**: Messages and logs shipped to external telemetry service
5. **Portable**: Single binary or Docker container
6. **Extensible**: MCP-compatible tool protocol

---

## Deployment Modes

| Mode | Description |
|------|-------------|
| **Binary** | Single executable, runs in terminal or as daemon |
| **Docker** | Containerized, resource-limited, network-isolated |

---

## External Dependencies

| Dependency | Purpose |
|------------|---------|
| LLM API (Anthropic/OpenAI/Gemini) | Language model inference |
| Internet Gateway (future) | Mediates web_fetch/web_search tools |
| Telemetry Service | Receives messages and logs for analytics |
| SQLite / Filesystem | Session persistence |

See `design.png` for component diagram.

---

## Core Components

### 1. Agentfile Parser

Parses the Agentfile DSL into an executable workflow representation.

**Structures:**
- **Workflow**: Name, inputs, agents, goals, steps
- **Input**: Name, optional default value
- **Agent**: Name, prompt file path
- **Goal**: Name, outcome (inline or file path), optional agent list
- **Step**: Type (RUN/LOOP), name, goal list, optional iteration limit

### 2. Workflow Executor

Executes the parsed workflow:
1. Bind inputs to workflow
2. Execute steps in order
3. For RUN: execute goals sequentially
4. For LOOP: repeat goals until convergence or max iterations
5. Export telemetry throughout

### 3. Agent Core

Executes individual goals by orchestrating LLM and tools:
1. Build system prompt (include agent personas if USING clause)
2. Build user prompt from goal outcome + state context
3. Send to LLM
4. Execute tool calls
5. Loop until LLM signals goal achieved
6. Update state with results

### 4. Multi-Agent Orchestration

When a goal uses multiple agents:
1. Spawn parallel LLM calls, one per agent
2. Each call includes agent persona in system prompt
3. Collect all responses
4. Synthesize: send combined output to LLM for reconciliation
5. Update state with synthesized result

### 5. Convergence Detection

For LOOP steps, detect when to stop:
- **Explicit**: LLM response indicates goal achieved
- **Implicit**: No tool calls made (nothing left to do)
- **State unchanged**: No progress from previous iteration

### 6. LLM Provider

Interface for multi-provider abstraction (via Fantasy):
- Anthropic (Claude)
- OpenAI
- Google (Gemini)
- OpenRouter
- Local (Ollama)

### 7. Tool Registry

Manages available tools and enforces security policy.

### 8. Session Manager

Persists execution state for recovery and debugging.
- SQLite backend (primary)
- Filesystem backend (fallback)

### 9. Telemetry Exporter

Ships messages and logs to external service:
- **HTTP**: POST to telemetry endpoint
- **OTLP**: OpenTelemetry Protocol
- **File**: Local JSON lines (offline/debug)
- **Noop**: Disabled

---

## Built-in Tools

### File Tools

| Tool | Description | Default Permission |
|------|-------------|-------------------|
| `read` | Read file contents | Allow in workspace |
| `write` | Create/overwrite files | Allow in workspace |
| `edit` | Find-and-replace in files | Allow in workspace |
| `glob` | Pattern-based file search | Allow |
| `grep` | Regex content search | Allow |
| `ls` | List directory contents | Allow |

### Execution Tools

| Tool | Description | Default Permission |
|------|-------------|-------------------|
| `bash` | Execute shell commands | Allowlist only |

### Web Tools (Gateway-Mediated)

| Tool | Description | Default Permission |
|------|-------------|-------------------|
| `web_fetch` | Fetch URL via internet gateway | Allow, rate-limited |
| `web_search` | Search via internet gateway | Allow, rate-limited |

**Note**: Web tools route through an Internet Gateway service that controls allowed domains, rate limits, and content filtering.

### Memory Tools

| Tool | Description | Default Permission |
|------|-------------|-------------------|
| `memory_read` | Read from persistent key-value store | Allow |
| `memory_write` | Write to persistent key-value store | Allow |

---

## Security Model

Security policy is defined in a separate `policy.toml` file. See `policy.png` for diagram.

### Policy File Structure

```toml
# policy.toml

# Global defaults
default_deny = true  # Whitelist-only mode: tools must have explicit allow

[read]
enabled = true
allow = ["$WORKSPACE/**"]
deny = ["~/.ssh/*", "~/.aws/*", "~/.gnupg/*"]

[write]
enabled = true
allow = ["$WORKSPACE/**"]
deny = ["~/.ssh/*", "~/.aws/*", "/etc/*"]

[edit]
enabled = true
allow = ["$WORKSPACE/**"]
deny = ["~/.ssh/*", "~/.aws/*"]

[glob]
enabled = true
allow = ["**"]

[grep]
enabled = true
allow = ["**"]
deny = ["/etc/shadow", "/etc/passwd"]

[ls]
enabled = true
allow = ["**"]

[bash]
enabled = true
allowlist = [
  "ls *", "pwd", "cat *", "head *", "tail *",
  "git status", "git log *", "git diff *",
  "go build *", "go test *", "go run *",
  "npm test *", "npm run *",
  "make *",
]
denylist = [
  "rm -rf /", "rm -rf /*",
  "sudo *", "su *",
  "curl * | bash", "wget * | bash",
]

[web_fetch]
enabled = true
allow_domains = ["*"]
rate_limit = 10  # requests per minute

[web_search]
enabled = true
rate_limit = 5

[memory_read]
enabled = true

[memory_write]
enabled = true
```

### Built-in Variables

| Variable | Expands to |
|----------|------------|
| `$WORKSPACE` | Agent's workspace directory |
| `~` | User home directory |

### Policy Semantics

| Setting | Behavior |
|---------|----------|
| `default_deny = true` | Tool must have explicit `allow` to work |
| `enabled = false` | Tool completely disabled |
| `allow` / `deny` | Allow checked first, deny wins on conflict |

### Glob Patterns

| Pattern | Matches |
|---------|---------|
| `*` | Single path segment |
| `**` | Recursive (any depth) |

### Bash-Specific Rules

- `allowlist`: Command patterns that are permitted
- `denylist`: Command patterns that are always blocked (checked first)
- Pattern matching uses glob syntax

---

## Transport

### stdio (JSON-RPC 2.0)

Primary interface. Compatible with MCP.

**Methods:**
- `run` — Execute workflow with inputs
- `event` — Stream execution events (goal_started, tool_call, goal_complete)

---

## Configuration

| Section | Fields |
|---------|--------|
| **agent** | id, workspace |
| **llm** | provider, model, api_key_env, max_tokens |
| **web** | gateway_url, gateway_token_env |
| **telemetry** | enabled, endpoint, protocol |
| **session** | store (sqlite/file), path |

---

## Module Structure

| Directory | Purpose |
|-----------|---------|
| `cmd/agent/` | Entry point, CLI |
| `internal/agentfile/` | Lexer, parser, AST |
| `internal/executor/` | Workflow executor, loop handling, convergence |
| `internal/agent/` | Core agent, multi-agent orchestration, synthesis |
| `internal/llm/` | Provider interface, Fantasy adapter |
| `internal/tools/` | Tool registry, security, individual tools |
| `internal/session/` | Session manager, storage backends |
| `internal/telemetry/` | Exporters (OTLP, HTTP, file) |
| `internal/config/` | Configuration loading |

---

## CLI Usage

| Command | Purpose |
|---------|---------|
| `agent run Agentfile --input key=value` | Run workflow |
| `agent run Agentfile --config agent.json` | Run with custom config |
| `agent validate Agentfile` | Validate syntax |
| `agent inspect Agentfile` | Show workflow structure |

---

## Telemetry Schema

All messages and logs shipped to telemetry service include:
- schema_version
- agent_id, session_id, workflow_name
- timestamp
- type (message, log)
- data: goal, role, agent, content, tool_calls, tokens, latency, model, iteration

---

# Agentfile Language Specification

## Overview

Agentfile is a declarative DSL for defining agent workflows. It specifies **what** to achieve, not **how**. The agent determines execution strategy.

## Design Principles

1. **Flat structure**: No indentation, no nesting
2. **One instruction per line**: Each line is a complete statement
3. **Declarative goals**: Define outcomes, agent figures out steps
4. **Implicit synthesis**: Multiple agents automatically reconcile
5. **Implicit failure handling**: Agent handles errors naturally
6. **Sequential by default**: RUN/LOOP blocks execute top to bottom
7. **External prompts**: Use FROM to keep prompts in separate files

---

## Keywords

### NAME

Defines the workflow name.

```
NAME <workflow-name>
```

### INPUT

Defines input parameters.

```
INPUT <name>
INPUT <name> DEFAULT <value>
```

### AGENT

Defines an agent persona. Prompt loaded from external file.

```
AGENT <name> FROM <path>
```

Example:
```
AGENT creative FROM agents/creative.md
AGENT devils_advocate FROM agents/devils_advocate.md
```

### GOAL

Defines a named goal (desired outcome). Prompt can be inline or from file.

```
GOAL <name> "<outcome>"
GOAL <name> FROM <path>
GOAL <name> "<outcome>" USING <agent1>, <agent2>, ...
GOAL <name> FROM <path> USING <agent1>, <agent2>, ...
```

Examples:
```
GOAL analyze "Understand $feature_request" USING creative, devils_advocate
GOAL analyze FROM goals/analyze.md USING creative, devils_advocate
GOAL run_tests "Run all tests and capture failures"
GOAL fix FROM goals/fix.md
```

**Variable interpolation**: Use `$name` to reference inputs or prior goal outputs.

**USING clause**: When multiple agents are specified, each works the goal in parallel, then agent synthesizes.

### RUN

Executes goals sequentially.

```
RUN <name> USING <goal1>, <goal2>, ...
```

### LOOP

Executes goals repeatedly until convergence or max iterations.

```
LOOP <name> USING <goal1>, <goal2>, ... WITHIN <n>
```

---

## File Structure

Two styles are valid:

**Style A**: All definitions first, execution at end

**Style B**: Interleaved (define goals, then RUN/LOOP, then more goals, then more RUN/LOOP)

Parser handles both. Rules:
1. GOAL defines a block (can appear anywhere before its use)
2. RUN/LOOP executes blocks (must reference already-defined goals)
3. RUN/LOOP execute in file order

---

## Grammar (EBNF)

```
agentfile       = { statement }
statement       = name_stmt | input_stmt | agent_stmt | goal_stmt | run_stmt | loop_stmt | comment | empty_line
name_stmt       = "NAME" identifier
input_stmt      = "INPUT" identifier [ "DEFAULT" value ]
agent_stmt      = "AGENT" identifier "FROM" path
goal_stmt       = "GOAL" identifier ( string | "FROM" path ) [ using_clause ]
run_stmt        = "RUN" identifier "USING" identifier_list
loop_stmt       = "LOOP" identifier "USING" identifier_list "WITHIN" ( number | variable )
using_clause    = "USING" identifier_list
identifier_list = identifier { "," identifier }
identifier      = letter { letter | digit | "_" }
variable        = "$" identifier
string          = '"' { character } '"'
path            = { path_char }
value           = number | string | identifier
number          = digit { digit }
comment         = "#" { character } newline
```

---

## Execution Model

1. **Parse**: Read Agentfile into AST
2. **Load external files**: Resolve all FROM paths, load prompt content
3. **Validate**: Check all referenced goals/agents exist
4. **Bind inputs**: Map provided inputs to variables
5. **Execute steps**: Process RUN/LOOP in file order
6. **For each RUN**: Execute goals sequentially
7. **For each LOOP**: Repeat goals until convergence or max iterations
8. **For each GOAL**:
   - If USING clause: spawn parallel agents, synthesize
   - Build prompt from outcome + execution state
   - Send to LLM, execute tool calls
   - Loop until LLM signals goal achieved
9. **Export telemetry**: Throughout execution

---

## Directory Structure

Recommended layout for a workflow:

```
my-workflow/
├── Agentfile
├── policy.toml
├── agents/
│   ├── creative.md
│   └── devils_advocate.md
└── goals/
    ├── analyze.md
    ├── system_tests.md
    ├── unit_tests.md
    ├── analyze_failures.md
    └── fix.md
```

---

## Example

See `Agentfile` and `agents/`, `goals/` directories for a complete TDD workflow example.
