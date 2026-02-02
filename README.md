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
    "api_key_env": "ANTHROPIC_API_KEY",
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
      "model": "gpt-4o",
      "api_key_env": "OPENAI_API_KEY"
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
# Validate first
./agent validate Agentfile

# Run the workflow
./agent run Agentfile --config agent.json

# Or with custom input
./agent run Agentfile --config agent.json --input topic="Rust programming"
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

# Agents reference prompts, skills, or packages
AGENT researcher FROM agents/researcher.md       # Markdown prompt file
AGENT critic FROM skills/code-review             # Skill directory (has SKILL.md)
AGENT helper FROM testing                        # Skill name (looked up in skills.paths)
AGENT scanner FROM security.agent REQUIRES "reasoning-heavy"  # Packaged agent

# Goals define what to accomplish
GOAL name "Inline description with $variables"
GOAL name FROM path/to/goal.md
GOAL name "Description" USING agent1, agent2  # Multi-agent (spawns isolated sub-agents)

# Steps execute goals
RUN step_name USING goal1, goal2, goal3
LOOP step_name USING goal1 WITHIN 5           # Loop max 5 times
LOOP step_name USING goal1 WITHIN $max_iter   # Variable limit
```

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

## Examples

See the `examples/` directory for 35 example workflows covering:
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

