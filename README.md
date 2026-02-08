# Headless Agent

A Go-based headless agent for workflow automation using LLMs.

> ⚠️ **Disclaimer:** This entire codebase was **gramcoded** — 20 hours of free time across 5 days, texting an AI through Telegram. No IDE. No terminal. Just a couch, a phone, and [OpenClaw](https://github.com/openclaw/openclaw) + Claude Opus 4.5. **Review before production use**.

## Quick Start

### Option A: Interactive Setup (Recommended)

```bash
# Build
make build

# Run the interactive setup wizard
./bin/agent setup
```

The setup wizard guides you through configuration for any deployment scenario — from local experimentation to enterprise Kubernetes deployments.

### Option B: Manual Setup

#### 1. Build

```bash
# Using Make (recommended)
make build          # Build to ./bin/agent
make install        # Install to ~/.local/bin/agent

# Or manually
cd src
go build -o agent ./cmd/agent
```

#### 2. Create a Config File

Create `agent.toml`:

```toml
# Agent Configuration

[agent]
id = "my-agent"
workspace = "/path/to/your/workspace"

[llm]
model = "claude-sonnet-4-20250514"
max_tokens = 4096

[profiles.reasoning-heavy]
model = "claude-opus-4-20250514"

[profiles.fast]
model = "gpt-4o-mini"

[profiles.code-generation]
model = "gemini-1.5-pro"

[session]
store = "file"
path = "./sessions"

[telemetry]
enabled = false
```

**Note:** The `provider` field is optional — it's automatically inferred from the model name:
- `claude-*` → Anthropic
- `gpt-*`, `o1-*`, `o3-*` → OpenAI
- `gemini-*`, `gemma-*` → Google
- `mistral-*`, `mixtral-*`, `codestral-*` → Mistral

Set `provider` explicitly for:
- Ambiguous model names
- OpenAI-compatible endpoints (Groq, OpenRouter, LiteLLM)
- Local Ollama (`ollama` with `base_url`)
- Ollama Cloud (`ollama-cloud`)

#### 3. Set API Keys

API keys are loaded in this priority order (highest to lowest):
1. Environment variables
2. `.env` file in current directory
3. `~/.config/grid/credentials.toml`

**Option A — Environment variables (recommended for production):**
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

**Option C — `~/.config/grid/credentials.toml` (shared credentials file):**
```toml
# ~/.config/grid/credentials.toml

# Simple: single key for all providers
[llm]
api_key = "your-api-key"

# Or provider-specific (optional, overrides [llm])
[anthropic]
api_key = "anthropic-specific-key"

[openai]
api_key = "openai-specific-key"
```

Priority: `[provider]` section → `[llm]` section → environment variable.

This file is shared with other Grid tools and agents.

#### Security Considerations

**For development and small deployments:** Using a credentials file is convenient. You can package it into a container for a small set of agents.

**For production and larger deployments:** Use environment variable injection instead of copying credential files. In Kubernetes, use Secrets mounted as env vars. Credential files:
- Can accidentally be committed to version control
- Are harder to rotate across multiple deployments
- Create copies of secrets that may persist on disk

**Never commit credentials to git.** Add `credentials.toml`, `.env`, and `*.pem` to your `.gitignore`.

#### 4. Create an Agentfile

Create `Agentfile`:

```
NAME hello-world
INPUT topic DEFAULT "Go programming"
GOAL research "Research $topic and list 3 key facts"
GOAL summarize "Summarize the research in one sentence"
RUN main USING research, summarize
```

#### 5. Run

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
| `agent setup` | Interactive setup wizard |
| `agent help` | Show help |
| `agent version` | Show version |

## Makefile Targets

```bash
make build          # Build to ./bin/agent
make install        # Install to ~/.local/bin/agent
make install-system # Install to /usr/local/bin (requires sudo)
make test           # Run all tests
make test-cover     # Run tests with coverage report
make docker-build   # Build Docker image
make clean          # Remove build artifacts
make help           # Show all available targets
```

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

## Security Framework

The agent includes a comprehensive security framework to defend against prompt injection and other attacks when processing untrusted content.

### Key Features

- **Trust-Tagged Content Blocks**: All content is tagged with trust levels (`trusted`, `vetted`, `untrusted`) and types (`instruction`, `data`)
- **Tiered Verification Pipeline**: Three-tier verification for tool calls involving untrusted content
- **Encoded Content Detection**: Detects Base64, hex, and URL encoding that could hide malicious payloads
- **Pattern Detection**: Identifies suspicious patterns like command injection, path traversal, and privilege escalation attempts
- **Cryptographic Audit Trail**: Ed25519 signatures for all security decisions

### Verification Tiers

| Tier | Method | Cost | When Used |
|------|--------|------|-----------|
| **Tier 1** | Deterministic checks | Free | Always (patterns, entropy, trust) |
| **Tier 2** | Cheap model triage | Low | If T1 flags concerns (default mode) |
| **Tier 3** | Full supervisor | High | If T2 flags concerns, or paranoid mode |

### Security Modes

- **default**: Efficient tiered verification (T1 → T2 → T3)
- **paranoid**: Skip T2, all flagged content goes directly to T3 supervision
- **research**: Security research mode with scope-aware supervision (see below)

### Configuration

Add to `agent.toml`:

```toml
[security]
mode = "default"          # or "paranoid"
user_trust = "untrusted"  # trust level for user messages
```

### Security Research Mode

For legitimate security research (pentesting, vulnerability assessment, malware analysis), use research mode in your Agentfile:

```
SECURITY research "authorized pentest of internal lab network 192.168.100.0/24"
```

Research mode:
- Uses scope-aware supervision (permissive within scope, strict at boundaries)
- Injects defensive framing into prompts
- Maintains full audit trail
- Still blocks actions outside declared scope

See `examples/security-research/` for security research workflow examples.

### High-Risk Tools

These tools receive extra scrutiny when untrusted content is in context:
- `bash` - Shell command execution
- `write` - File system writes  
- `web_fetch` - External HTTP requests
- `spawn_agent` - Sub-agent creation

See `examples/security/` for example configurations and `docs/security/` for detailed documentation.

## Agentfile Syntax

```
# Comments start with #

NAME workflow-name

# Inputs with optional defaults
INPUT required_param
INPUT optional_param DEFAULT "value"

# Global setting - execution is supervised
SUPERVISED

# Security mode (optional - defaults to "default")
SECURITY default    # or: SECURITY paranoid
SECURITY research "scope description"  # for security research

# Agents reference prompts, skills, or packages (with optional structured output)
AGENT researcher FROM agents/researcher.md -> findings, sources
AGENT critic FROM skills/code-review -> issues, recommendations
AGENT helper "Inline prompt for helper" -> result
AGENT scanner FROM security.agent REQUIRES "reasoning-heavy"

# Goals define what to accomplish (with optional structured output)
# Supervision modifiers: SUPERVISED (LLM) or SUPERVISED HUMAN
GOAL name "Inline description with $variables" -> output1, output2
GOAL name FROM path/to/goal.md -> outputs
GOAL name "Description" -> summary, recommendations USING agent1, agent2
GOAL sensitive_task "Handle with care" SUPERVISED           # LLM supervision
GOAL critical_task "Requires human approval" SUPERVISED HUMAN

# Mark goal as UNSUPERVISED when SUPERVISED is applied globally.
GOAL fast_task "No supervision needed" UNSUPERVISED

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

The agent uses [Catwalk](https://github.com/charmbracelet/catwalk) for model discovery and [Fantasy](https://charm.land/fantasy) for provider abstraction.

| Provider | `provider` value | `api_key_env` | Notes |
|----------|-----------------|---------------|-------|
| Anthropic | `anthropic` | `ANTHROPIC_API_KEY` | Claude models |
| OpenAI | `openai` | `OPENAI_API_KEY` | GPT models |
| Google | `google` | `GOOGLE_API_KEY` | Gemini models |
| Groq | `groq` | `GROQ_API_KEY` | Fast inference |
| Mistral | `mistral` | `MISTRAL_API_KEY` | Mistral models |
| Ollama (local) | `ollama` | — | Requires `base_url` |
| Ollama Cloud | `ollama-cloud` | `OLLAMA_API_KEY` | Hosted models |

### Custom Endpoints (OpenRouter, LiteLLM, Ollama, etc.)

Use `base_url` to connect to any OpenAI-compatible endpoint:

```toml
# agent.toml

# OpenRouter
[llm]
provider = "openrouter"
model = "anthropic/claude-3.5-sonnet"
base_url = "https://openrouter.ai/api/v1"

# LiteLLM proxy
[llm]
provider = "litellm"
model = "gpt-4"
base_url = "http://localhost:4000"

# Local Ollama (uses OpenAI-compatible endpoint)
[llm]
provider = "ollama"
model = "llama3:70b"
base_url = "http://localhost:11434/v1"

# LMStudio
[llm]
provider = "lmstudio"
model = "local-model"
base_url = "http://localhost:1234/v1"

# Generic OpenAI-compatible
[llm]
provider = "openai-compat"
model = "any-model"
base_url = "https://my-api.example.com/v1"
```

### Ollama Cloud

Ollama Cloud provides hosted models accessible via API. Use the native `ollama-cloud` provider:

```toml
# agent.toml
[llm]
provider = "ollama-cloud"
model = "gpt-oss:120b"  # or any model from ollama.com/search?c=cloud
```

```toml
# credentials.toml
[ollama-cloud]
api_key = "your-ollama-api-key"  # from ollama.com/settings/keys
```

Available models: [ollama.com/search?c=cloud](https://ollama.com/search?c=cloud)

**Note:** This is different from local Ollama. For local Ollama, use `provider = "ollama"` with `base_url = "http://localhost:11434/v1"`.

### Proxying Native Providers

You can also use `base_url` with native providers to proxy requests:

```toml
[llm]
provider = "openai"
model = "gpt-4"
base_url = "https://my-company-proxy.com/v1"
```

### Model Discovery

The `provider` field is **optional** — the agent automatically determines the provider:

1. **Catwalk lookup** (if `CATWALK_URL` set): Queries the catwalk server for exact model → provider mapping
2. **Pattern inference** (fallback): Uses model name prefixes:
   - `claude-*` → anthropic
   - `gpt-*`, `o1-*`, `o3-*` → openai
   - `gemini-*`, `gemma-*` → google
   - `mistral-*`, `mixtral-*`, `codestral-*` → mistral

When using Catwalk, you also get:
- Model context windows and token limits
- Cost information
- Capability flags (reasoning, attachments)

**Running a local Catwalk server:**
```bash
# Optional — only needed for live model updates
export CATWALK_URL=http://localhost:8080
```

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

## Security

### Credentials

**File permissions:** `credentials.toml` must have mode 0400 (owner read-only). The agent will refuse to load credentials with insecure permissions.

```bash
chmod 400 ~/.config/grid/credentials.toml
```

**Priority order:**
1. `credentials.toml` (if found with secure permissions)
2. Environment variables (fallback)

### Protected Config Files

The agent cannot modify its own configuration files:
- `agent.toml` — agent configuration
- `policy.toml` — security policy
- `credentials.toml` — API keys

This prevents privilege escalation attacks where an agent tries to grant itself additional permissions.

### MCP Tool Security

MCP servers run with the agent's permissions. For production, restrict which tools can be called:

```toml
# policy.toml
[mcp]
default_deny = true  # Block all MCP tools by default
allowed_tools = [
  "filesystem:read_file",
  "filesystem:list_directory",
  "memory:*",  # Allow all tools from memory server
]
```

If `[mcp]` is not configured, the agent logs a security warning and allows all MCP tools (development mode).

### Web Tool Security

Restrict which domains can be accessed:

```toml
# policy.toml
[web_fetch]
enabled = true
allow_domains = ["github.com", "*.github.io", "docs.python.org"]
```

Without `allow_domains`, all domains are allowed.

### Shell Command Security

Restrict shell commands with allowlists/denylists:

```toml
[bash]
enabled = true
allowlist = ["ls *", "cat *", "go build *", "go test *"]
denylist = ["rm -rf *", "sudo *", "curl * | bash"]
```

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

## Supervision

Enable drift detection and course correction for critical workflows. The supervision system monitors agent execution and intervenes when the agent's understanding diverges from the original goal.

### Enabling Supervision

**Global supervision (all steps):**
```
SUPERVISED
NAME my-workflow
GOAL analyze "Analyze data"
GOAL report "Generate report"
RUN main USING analyze, report
```

**Per-step supervision:**
```
NAME my-workflow
GOAL analyze "Analyze data"
GOAL deploy "Deploy to production" SUPERVISED HUMAN
RUN main USING analyze, deploy
```

**Opt-out for specific steps:**
```
SUPERVISED
NAME my-workflow
GOAL analyze "Analyze data"
GOAL trivial "Quick cleanup" UNSUPERVISED
RUN main USING analyze, trivial
```

### Supervision Modes

| Mode | Behavior |
|------|----------|
| `SUPERVISED` | Autonomous — supervisor corrects drift automatically |
| `SUPERVISED HUMAN` | Requires human approval — fails if no human available |
| `UNSUPERVISED` | Skips supervision for trusted/fast operations |

### Four-Phase Execution

When supervision is enabled, each step executes in four phases:

1. **COMMIT** — Agent declares intent before execution
   - Interpretation of the goal
   - Planned approach
   - Expected output
   - Assumptions made

2. **EXECUTE** — Agent performs the work
   - Tools called
   - Actual output
   - Self-assessment of whether commitment was met

3. **RECONCILE** — Static pattern matching (fast, no LLM)
   - Concerns raised?
   - Commitment met?
   - Deviations from plan?
   - Low confidence?

4. **SUPERVISE** — LLM evaluation (only if reconcile triggers)
   - Evaluates drift against original goal
   - Decides: CONTINUE, REORIENT, or PAUSE

### Pre-flight Check

Before execution begins, the system checks if:
- Any step requires `SUPERVISED HUMAN`
- A human connection (ACP/A2A) is available

If human required but unavailable → **hard fail before execution starts**.

This prevents wasted compute on workflows that would fail mid-run.

### Example: Deployment Pipeline

```
SUPERVISED
NAME deployment-pipeline
INPUT service_name

# Build and test — supervised for drift detection
GOAL build "Build $service_name"
GOAL test "Run test suite"

# Security scan — important, keep supervised
GOAL security "Scan for vulnerabilities"

# Config validation — fast and trusted
GOAL config "Validate config" UNSUPERVISED

# Deployment — critical, requires human approval
GOAL deploy "Deploy $service_name" SUPERVISED HUMAN

# Post-deploy verification
GOAL verify "Run smoke tests"

RUN pipeline USING build, test, security, config, deploy, verify
```

This workflow:
- Supervises most steps (inherited from global)
- Skips supervision for trivial config validation
- Requires human approval before deployment
- Fails at startup if no human connection available

See `examples/39-supervision/` for more examples.

