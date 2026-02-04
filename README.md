# Headless Agent

A Go-based headless agent for workflow automation using LLMs.

## Quick Start

### 1. Build

```bash
cd src
go build -o agent ./cmd/agent
```

### 2. Create a Config File

Create `agent.json`:

```json
{
  "agent": {
    "id": "my-agent",
    "workspace": "/path/to/your/workspace"
  },
  "llm": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 4096
  },
  "profiles": {
    "reasoning-heavy": {
      "model": "claude-opus-4-20250514"
    },
    "fast": {
      "model": "claude-haiku-20240307"
    },
    "code-generation": {
      "provider": "openai",
      "model": "gpt-4o"
    }
  },
  "session": {
    "store": "file",
    "path": "./sessions"
  },
  "telemetry": {
    "enabled": false
  }
}
```

**Note:** `api_key_env` is optional. By default, the agent uses standard environment variables based on the provider (e.g., `ANTHROPIC_API_KEY` for Anthropic). Only set `api_key_env` if you need a custom variable name.

### 3. Set API Keys

API keys are loaded in this priority order (highest to lowest):
1. Environment variables
2. `.env` file in current directory
3. `~/.config/grid/credentials.toml`

**Option A — Environment variables:**
```bash
export ANTHROPIC_API_KEY="your-key-here"
export OPENAI_API_KEY="your-key-here"
```

**Option B — `.env` file (auto-loaded from current directory):**
```bash
# .env
ANTHROPIC_API_KEY=your-key-here
OPENAI_API_KEY=your-key-here
```

**Option C — `~/.config/grid/credentials.toml` (recommended for shared credentials):**
```toml
# ~/.config/grid/credentials.toml

[anthropic]
api_key = "your-key-here"

[openai]
api_key = "your-key-here"

[google]
api_key = "your-key-here"

[mistral]
api_key = "your-key-here"

[groq]
api_key = "your-key-here"
```

This file is shared with other Grid tools and agents.

### 4. Create an Agentfile

Create `Agentfile`:

```
NAME hello-world
INPUT topic DEFAULT "Go programming"
GOAL research "Research $topic and list 3 key facts"
GOAL summarize "Summarize the research in one sentence"
RUN main USING research, summarize
```

### 5. Run

```bash
# Validate (uses ./Agentfile by default)
./agent validate

# Or specify a different file
./agent validate -f path/to/MyAgentfile

# Run the workflow
./agent run --config agent.json

# Run with custom input
./agent run --config agent.json --input topic="Rust programming"

# Run a specific Agentfile
./agent run -f examples/hello.agent --config agent.json
```

### Quick Test (Validate Examples)

```bash
cd src

# Build
go build -o agent ./cmd/agent

# Validate all examples
for f in ../examples/*.agent; do ./agent validate "$f"; done

# Run tests
go test ./...
```

## Capability Profiles

Agents can declare capability requirements using `REQUIRES`. The config maps these to specific LLM providers/models:

**Agentfile:**
```
AGENT critic FROM agents/critic.md REQUIRES "reasoning-heavy"
AGENT helper FROM agents/helper.md REQUIRES "fast"
```

**Config profiles:**
```json
{
  "profiles": {
    "reasoning-heavy": {
      "provider": "anthropic",
      "model": "claude-opus-4-20250514"
    },
    "fast": {
      "model": "claude-haiku-20240307"
    }
  }
}
```

**Benefits:**
- Workflow declares *intent* (what capability is needed)
- Config controls *implementation* (which model provides it)
- Same Agentfile works in different environments
- Ops can control costs without editing workflows

**Profile inheritance:** Profiles inherit from the default `llm` config. Only specify what differs.

## CLI Commands

| Command | Description |
|---------|-------------|
| `agent run <file>` | Execute a workflow |
| `agent validate <file>` | Check syntax without running |
| `agent inspect <file>` | Show workflow/package structure |
| `agent pack <dir>` | Create a signed package |
| `agent verify <pkg>` | Verify package signature |
| `agent install <pkg>` | Install a package |
| `agent keygen` | Generate signing key pair |
| `agent help` | Show help |
| `agent version` | Show version |

## Flags

| Flag | Description |
|------|-------------|
| `--config <path>` | Config file (default: `agent.json`) |
| `--input key=value` | Provide input (repeatable) |
| `--policy <path>` | Security policy file |
| `--workspace <path>` | Override workspace directory |

## Packaging

Create distributable, signed agent packages.

### Generate Signing Keys

```bash
./agent keygen -o my-key
# Creates: my-key.pem (private, keep secret!) and my-key.pub (share for verification)
```

### Create a Package

```bash
./agent pack ./my-agent \
  --sign my-key.pem \
  --author "Your Name" \
  --email "you@example.com" \
  --license MIT \
  -o my-agent-1.0.0.agent
```

### Package Structure

```
my-agent/
├── Agentfile           # Required: workflow definition
├── manifest.json       # Optional: additional metadata (outputs, deps)
├── agents/             # Agent persona files
├── goals/              # Goal prompt files
└── policy.toml         # Default security policy
```

### Manifest (Optional)

Create `manifest.json` for additional metadata not in Agentfile:

```json
{
  "name": "my-agent",
  "version": "1.0.0",
  "description": "What this agent does",
  "outputs": {
    "report": {"type": "string", "description": "Generated report"}
  },
  "dependencies": {
    "helper-agent": "^1.0.0"
  }
}
```

Note: `name`, `version`, `inputs`, and `requires` are auto-extracted from Agentfile.

### Verify a Package

```bash
./agent verify my-agent-1.0.0.agent --key author.pub
```

### Inspect a Package

```bash
./agent inspect my-agent-1.0.0.agent
```

### Install a Package

```bash
# Install with dependencies
./agent install my-agent-1.0.0.agent

# Skip dependencies (for A2A setups)
./agent install my-agent-1.0.0.agent --no-deps

# Preview what would be installed
./agent install my-agent-1.0.0.agent --dry-run
```

Packages install to `~/.agent/packages/<name>/<version>/`.

## Security Policy

Create `policy.toml` to restrict tool access:

```toml
default_deny = true
workspace = "/path/to/workspace"

[tools.read]
enabled = true
allow = ["$WORKSPACE/**"]
deny = ["**/.env", "**/*.key"]

[tools.write]
enabled = true
allow = ["$WORKSPACE/**"]

[tools.bash]
enabled = true
allowlist = ["ls *", "cat *", "grep *"]
denylist = ["rm *", "sudo *"]

[tools.web_fetch]
enabled = true
allow_domains = ["api.github.com", "*.example.com"]
rate_limit = 10
```

## Agentfile Syntax

```
# Comments start with #

NAME workflow-name

# Inputs with optional defaults
INPUT required_param
INPUT optional_param DEFAULT "value"

# Agents reference prompts, skills, or packages (with optional structured output)
AGENT researcher FROM agents/researcher.md -> findings, sources
AGENT critic FROM skills/code-review -> issues, recommendations
AGENT helper "Inline prompt for helper" -> result
AGENT scanner FROM security.agent REQUIRES "reasoning-heavy"

# Goals define what to accomplish (with optional structured output)
GOAL name "Inline description with $variables" -> output1, output2
GOAL name FROM path/to/goal.md -> outputs
GOAL name "Description" -> summary, recommendations USING agent1, agent2

# Steps execute goals
RUN step_name USING goal1, goal2, goal3
LOOP step_name USING goal1 WITHIN 5           # Loop max 5 times
LOOP step_name USING goal1 WITHIN $max_iter   # Variable limit
```

## Structured Output

Use `->` to declare output fields. The LLM returns JSON with those fields, which become variables for subsequent goals.

### Simple structured output

```
GOAL research "Research $topic" -> findings, sources, confidence
GOAL report "Write report using $findings" -> summary, action_items
```

- `research` goal returns: `{"findings": "...", "sources": [...], "confidence": 0.8}`
- Fields become variables: `$findings`, `$sources`, `$confidence`
- `report` goal can use `$findings` in its prompt

### Multi-agent with synthesis

```
AGENT researcher "Research $topic" -> findings, sources
AGENT critic "Find biases in $topic" -> issues, concerns

GOAL analyze "Analyze $topic" -> summary, recommendations USING researcher, critic
```

1. `researcher` and `critic` run in **parallel**
2. Each returns structured JSON
3. An implicit **synthesizer** transforms their outputs into the goal's fields
4. This is essentially: `findings, sources, issues, concerns -> summary, recommendations`

## AGENT FROM Resolution

The `FROM` clause in `AGENT` declarations supports smart resolution:

| FROM Value | What Happens |
|------------|--------------|
| `agents/critic.md` | File path → loads as prompt text |
| `skills/code-review` | Directory with `SKILL.md` → loads as skill |
| `testing` | Name → searches `skills.paths` for matching skill directory |
| `scanner.agent` | Package file → loads as sub-agent (must be packaged, not raw Agentfile) |

**Resolution order:**
1. Check if path exists relative to Agentfile
2. If file → load as prompt (must be `.md`)
3. If directory → must have `SKILL.md`, loads as skill
4. If not found → search configured `skills.paths`
5. If still not found → error

**Configure skill paths in `agent.json`:**
```json
{
  "skills": {
    "paths": ["./skills", "~/.agent/skills", "/opt/agent-skills"]
  }
}
```

## Sub-Agents

When a goal uses `USING agent1, agent2`, each agent runs as a **true sub-agent** in complete isolation:

- **Own tools** — Only tools declared in the sub-agent's package
- **Own memory** — Fresh context, no shared state
- **Own execution loop** — Runs to completion independently
- **Own context window** — Separate from orchestrator

### Sub-Agent Architecture

```
Orchestrator (Agentfile)
    ├── spawns → Agent A (isolated .agent package)
    ├── spawns → Agent B (isolated .agent package)
    └── synthesizes results when both complete
```

### Constraints

- **No nesting** — Sub-agents cannot spawn their own sub-agents
- **No shared state** — Parent passes input, child returns output
- **Orchestrator coordinates** — Only the top-level workflow spawns agents

### Example

**orchestrator/Agentfile:**
```
NAME code-review
INPUT code_path

AGENT security FROM security-scanner.agent REQUIRES "reasoning-heavy"
AGENT style FROM style-checker.agent REQUIRES "fast"

GOAL audit "Review $code_path" USING security, style
GOAL report "Generate combined report from findings"

RUN main USING audit, report
```

**security-scanner.agent** (packaged agent):
```
NAME security-scanner
GOAL scan "Scan for vulnerabilities in $code_path"
RUN main USING scan
```

When the orchestrator runs `audit`, it:
1. Spawns `security-scanner.agent` with `code_path` as input
2. Spawns `style-checker.agent` in parallel
3. Waits for both to complete
4. Synthesizes their outputs

## Dynamic Sub-Agent Spawning

In addition to static sub-agents declared in the Agentfile, the LLM can **dynamically spawn sub-agents** at runtime using the `spawn_agent` tool.

### How It Works

Every agent has access to the `spawn_agent(role, task, outputs)` tool. The system prompt includes orchestrator guidance that encourages delegation when appropriate.

The optional `outputs` parameter enables structured output — when provided, the sub-agent returns JSON with those fields instead of freeform text.

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

### Example

**Agentfile:**
```
NAME dynamic-research
INPUT topic
GOAL research "Research $topic thoroughly, considering multiple perspectives"
RUN main USING research
```

**What happens:**
The LLM receives the goal and decides to delegate:
```
spawn_agent(role: "researcher", task: "Find factual information about {topic}")
spawn_agent(role: "critic", task: "Identify potential biases and limitations")
spawn_agent(role: "synthesizer", task: "Combine findings into a balanced summary")
```

### Depth=1 Enforcement

Sub-agents spawned dynamically **cannot spawn their own sub-agents**. The `spawn_agent` tool is automatically excluded from their tool set:

```
Orchestrator (has spawn_agent)
    ├── researcher (no spawn_agent)
    ├── critic (no spawn_agent)
    └── synthesizer (no spawn_agent)
```

This prevents infinite recursion and keeps the execution model simple.

### When to Use

| Static (AGENT/USING) | Dynamic (spawn_agent) |
|---------------------|----------------------|
| Known agents at design time | LLM decides what's needed |
| Packaged agents with policies | Ad-hoc specialists |
| Explicit control | Flexible problem decomposition |

Both approaches can be used in the same workflow.

## Docker

```bash
# Build
docker build -t headless-agent src/

# Run
docker run -it --rm \
  -v $(pwd):/workspace \
  -e ANTHROPIC_API_KEY \
  headless-agent run /workspace/Agentfile
```

## Supported LLM Providers

| Provider | `provider` value | `api_key_env` |
|----------|-----------------|---------------|
| Anthropic | `anthropic` | `ANTHROPIC_API_KEY` |
| OpenAI | `openai` | `OPENAI_API_KEY` |

## Built-in Tools

### File Operations
- `read` — Read file contents
- `write` — Write/create files
- `edit` — Edit files (find/replace)
- `glob` — Find files by pattern
- `grep` — Search file contents
- `ls` — List directory contents

### Shell
- `bash` — Execute shell commands (policy-controlled)

### Web
- `web_search` — Search the web
- `web_fetch` — Fetch and summarize URL content

### Memory
- `memory_read` — Read from agent memory
- `memory_write` — Write to agent memory

### Dynamic Agents
- `spawn_agent` — Spawn sub-agents at runtime (see Dynamic Sub-Agent Spawning)

### Web Search Providers

`web_search` supports multiple providers with automatic fallback:

| Priority | Provider | API Key Required | Notes |
|----------|----------|------------------|-------|
| 1 | Brave Search | `BRAVE_API_KEY` | Best quality |
| 2 | Tavily | `TAVILY_API_KEY` | Good for research |
| 3 | DuckDuckGo | None | Zero-config fallback |

Configure in `~/.config/grid/credentials.toml`:
```toml
[brave]
api_key = "your-key"

[tavily]
api_key = "your-key"
```

If no API keys are configured, `web_search` automatically uses DuckDuckGo's HTML endpoint.

## Examples

See the `examples/` directory for 38 example workflows covering:
- Programming (code review, testing, refactoring, security audits)
- Writing (essays, stories, translations)
- Planning (travel, fitness, learning paths)
- Analysis (decisions, debates, dependencies)
- Multi-agent collaboration and parallel specialists
- Skill-based agents and mixed sources

## Protocol Support

### MCP (Model Context Protocol)

Connect to external MCP tool servers for additional capabilities:

```json
{
  "mcp": {
    "servers": {
      "filesystem": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
      },
      "memory": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-memory"]
      },
      "github": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-github"],
        "env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}
      }
    }
  }
}
```

Tools from MCP servers are automatically discovered and made available to workflows.

### Agent Skills (agentskills.io)

Load skills from directories containing `SKILL.md`:

```json
{
  "skills": {
    "paths": ["./skills", "~/.agent/skills"]
  }
}
```

**Skill structure:**
```
my-skill/
├── SKILL.md           # Required: frontmatter + instructions
├── scripts/           # Optional: executable scripts
├── references/        # Optional: additional docs
└── assets/            # Optional: templates, data
```

**SKILL.md format:**
```markdown
---
name: my-skill
description: What this skill does and when to use it.
license: MIT
---

# Instructions

Step-by-step instructions for the agent...
```

### ACP (Agent Client Protocol)

The agent can run as an ACP server for editor integration:

```bash
./agent acp --config agent.json
```

This enables communication with code editors (VS Code, JetBrains, Zed) that support ACP.

