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

**Prerequisites** (for semantic memory with sqlite-vec):
```bash
# Debian/Ubuntu
sudo apt-get install libsqlite3-dev

# macOS
brew install sqlite3

# Or build without semantic memory (CGO_ENABLED=0)
```

```bash
# Using Make (recommended)
make build          # Build to ./bin/agent
make install        # Install to ~/.local/bin/agent

# Or manually
cd src
CGO_CFLAGS="-I/usr/include" go build -o agent ./cmd/agent
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
thinking = "auto"  # auto|off|low|medium|high

[profiles.reasoning-heavy]
model = "claude-opus-4-20250514"

[profiles.fast]
model = "gpt-4o-mini"

[profiles.code-generation]
model = "gemini-1.5-pro"

# Embedding providers for semantic memory.
#
# Supported:
#   - openai:  text-embedding-3-small, text-embedding-3-large
#   - google:  text-embedding-004
#   - mistral: mistral-embed
#   - cohere:  embed-english-v3.0, embed-multilingual-v3.0
#   - voyage:  voyage-2, voyage-large-2, voyage-code-2
#   - ollama:  nomic-embed-text, mxbai-embed-large (local)
#   - none:    Disables semantic memory (KV still works)
#
# NOT supported (no embedding endpoints):
#   - anthropic (Claude) - use voyage instead
#   - openrouter - chat completions only
#   - groq - chat completions only
#
[embedding]
provider = "openai"              # see supported list above
model = "text-embedding-3-small"

[storage]
path = "~/.local/grid"           # Base directory for sessions, memory, logs
persist_memory = true            # true = memory survives across runs

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

### Built-in LLM Roles

| Config Section | Purpose | When Used |
|----------------|---------|-----------|
| `[llm]` | Primary model | Main workflow execution, goals, sub-agents without REQUIRES |
| `[small_llm]` | Fast/cheap model | `web_fetch` summarization, security triage fallback |
| `[profiles.<name>]` | Capability-specific | Sub-agents with `REQUIRES "<name>"` |
| `[security] triage_llm` | Security triage | Tier 2 verification (points to a profile name) |

**Example config:**
```toml
[llm]
model = "claude-sonnet-4-20250514"
max_tokens = 4096

[small_llm]
model = "claude-haiku-20240307"
max_tokens = 1024

[profiles.reasoning-heavy]
model = "claude-opus-4-20250514"

[profiles.fast]
model = "gpt-4o-mini"

[profiles.code-generation]
model = "claude-sonnet-4-20250514"

[security]
triage_llm = "fast"  # Use the "fast" profile for security triage
```

## Adaptive Thinking

The agent supports thinking/reasoning modes that enable models to show their work on complex problems. Thinking is controlled per-request using a heuristic classifier — **zero extra LLM calls, just pattern matching**.

### Configuration

```toml
[llm]
model = "claude-sonnet-4-20250514"
thinking = "auto"  # auto|off|low|medium|high
```

| Value | Behavior |
|-------|----------|
| `auto` (default) | Heuristic classifier decides per-request |
| `off` | Never use thinking |
| `low` | Light reasoning (simple questions, few tools) |
| `medium` | Moderate reasoning (code, planning, analysis) |
| `high` | Deep reasoning (proofs, debugging, architecture) |

### How Auto Works

The classifier analyzes each request before sending:

**High complexity** (→ `high`):
- Math expressions, proofs, equations
- "debug", "why is", "root cause"
- "design system", "architecture", "trade-off"
- "security analysis", "threat model"
- Very long context (>3000 chars)
- Many tools (>10)

**Medium complexity** (→ `medium`):
- "implement", "refactor", "optimize"
- "step by step", "explain how"
- Code-related keywords
- Moderate context (>1000 chars)
- Several tools (>5)

**Low complexity** (→ `low`):
- "how to", "what is the best"
- "summarize", "list"
- A few tools (>2)

**Simple queries** (→ `off`):
- Greetings, short questions
- No complexity indicators

### Provider Support

| Provider | Thinking Support | Notes |
|----------|-----------------|-------|
| Anthropic | ✅ Extended thinking | Budget tokens auto-scaled by level |
| OpenAI | ✅ Reasoning effort | For o1/o3 models |
| Ollama Cloud | ✅ Think API | GPT-OSS uses levels, others use bool |
| Google | ❌ | Not yet supported |
| Groq/Mistral | ❌ | Not yet supported |

## Semantic Memory

The agent supports persistent memory across sessions with two components:

- **Photographic memory (KV)** — exact key-value recall, always available
- **Semantic memory** — insights and decisions with vector embeddings for meaning-based search

### Configuration

```toml
# Embedding providers for semantic memory.
#
# Supported:
#   - openai:  text-embedding-3-small, text-embedding-3-large
#   - google:  text-embedding-004
#   - mistral: mistral-embed
#   - cohere:  embed-english-v3.0, embed-multilingual-v3.0
#   - voyage:  voyage-2, voyage-large-2, voyage-code-2
#   - ollama:  nomic-embed-text, mxbai-embed-large (local)
#   - none:    Disables semantic memory (KV still works)
#
# NOT supported (no embedding endpoints):
#   - anthropic (Claude) - use voyage instead
#   - openrouter - chat completions only
#   - groq - chat completions only
#
[embedding]
provider = "openai"
model = "text-embedding-3-small"

[storage]
path = "~/.local/grid"           # Base directory for all persistent data
persist_memory = true            # true = memory survives across runs
                                 # false = session-scoped (scratchpad)
```

### Directory Structure

```
{storage.path}/
├── sessions/       # Session state (always persisted)
├── kv.json         # Photographic memory (if persist_memory=true)
├── semantic.db     # Semantic memory (if persist_memory=true)
└── logs/           # Audit logs
```

### Memory Tools

**Photographic (KV) — always available:**
| Tool | Purpose |
|------|---------|
| `memory_read` | Get value by exact key |
| `memory_write` | Store key-value pair |
| `memory_list` | List keys by prefix |
| `memory_search` | Substring search across keys/values |

**Semantic — requires embedding provider:**
| Tool | Purpose |
|------|---------|
| `memory_remember` | Store content with embeddings for semantic search |
| `memory_recall` | Find relevant memories by meaning |
| `memory_forget` | Delete a memory by ID |

**Example workflow:**
```
# Store an insight
memory_remember(
  content: "We decided to use PostgreSQL for better JSON support",
  importance: 0.8,
  tags: ["architecture", "database"]
)

# Later, recall it semantically
memory_recall(query: "database decision")
# Returns the PostgreSQL insight even without exact keyword match
```

### Disabling Semantic Memory

For resource-constrained environments, disable semantic memory:

```toml
[embedding]
provider = "none"
```

The agent will still have KV (photographic) memory but `memory_recall` semantic search will be unavailable.

See [docs/memory/semantic-memory.md](docs/memory/semantic-memory.md) for details.

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

Both static (`AGENT`/`USING`) and dynamic (`spawn_agent`) sub-agents use the **same execution path** with identical capabilities:

- **Parent's tools** — Full access to the parent's tool registry
- **MCP tools** — All configured MCP servers
- **Security verification** — 3-tier verification on all tool calls
- **Supervision phases** — COMMIT/EXECUTE/RECONCILE/SUPERVISE when enabled
- **Agentic loop** — Full tool-calling loop, not just a single LLM call
- **No nesting** — `spawn_agent`/`spawn_agents` excluded (depth=1)

### Static Sub-Agents (AGENT/USING)

Declare agents in Agentfile with personas and model requirements:

```
AGENT optimist FROM agents/optimist.md REQUIRES "reasoning-heavy"
AGENT critic FROM agents/devils-advocate.md REQUIRES "fast"

GOAL evaluate "Analyze this decision" USING optimist, critic
```

The `FROM` path loads a persona prompt. The `REQUIRES` profile selects which LLM to use.

When the goal runs:
1. Spawns both agents in parallel (each with full tool access)
2. Each runs a complete agentic loop with tool calls
3. Waits for both to complete
4. Synthesizes their outputs

### Dynamic Sub-Agents (spawn_agent)

The LLM can spawn sub-agents at runtime:

```
spawn_agent(role: "researcher", task: "Find facts about {topic}")
spawn_agents(agents: [
  {role: "optimist", task: "Make the case for..."},
  {role: "pessimist", task: "Make the case against..."}
])
```

The optional `outputs` parameter enables structured output parsing.

### Unified Execution

Both paths flow through the same code:

```
spawnAgentWithPrompt()
  └── subAgentExecutePhaseWithProvider()
        └── executeToolsParallel()
              └── executeTool()
                    └── verifyToolCall()  ← security
```

### When to Use

| Static (AGENT/USING) | Dynamic (spawn_agent) |
|---------------------|----------------------|
| Known agents at design time | LLM decides what's needed |
| Persona defined in files | Ad-hoc specialists |
| Different model per agent | Same model as parent |
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

The agent uses official SDKs where available:

| Provider | SDK | Notes |
|----------|-----|-------|
| Anthropic | [anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go) | Official SDK |
| OpenAI | [openai-go](https://github.com/openai/openai-go) | Official SDK |
| Google | [generative-ai-go](https://github.com/google/generative-ai-go) | Official Gemini SDK |
| Mistral | Native HTTP | OpenAI-compatible API |
| Groq | Native HTTP | OpenAI-compatible API |
| xAI | Native HTTP | OpenAI-compatible API |
| OpenRouter | Native HTTP | OpenAI-compatible API |
| Ollama Cloud | Native HTTP | Native Ollama API |
| Ollama Local | Native HTTP | OpenAI-compatible API |
| LMStudio | Native HTTP | OpenAI-compatible API |

| Provider | `provider` value | `api_key_env` | Notes |
|----------|-----------------|---------------|-------|
| Anthropic | `anthropic` | `ANTHROPIC_API_KEY` | Claude models |
| OpenAI | `openai` | `OPENAI_API_KEY` | GPT, o1, o3 models |
| Google | `google` | `GOOGLE_API_KEY` | Gemini models |
| Groq | `groq` | `GROQ_API_KEY` | Fast inference |
| Mistral | `mistral` | `MISTRAL_API_KEY` | Mistral models |
| xAI | `xai` | `XAI_API_KEY` | Grok models |
| OpenRouter | `openrouter` | `OPENROUTER_API_KEY` | Multi-provider routing |
| Ollama Local | `ollama-local` | — | Local, no API key |
| LMStudio | `lmstudio` | — | Local, no API key |
| Ollama Cloud | `ollama-cloud` | `OLLAMA_API_KEY` | Hosted models |
| Generic | `openai-compat` | — | Any OpenAI-compatible |

### OpenAI-Compatible Endpoints

Any service implementing the OpenAI chat completions API can be used. Built-in providers have default base URLs:

| Provider | Default Base URL |
|----------|-----------------|
| `groq` | api.groq.com/openai/v1 |
| `mistral` | api.mistral.ai/v1 |
| `xai` | api.x.ai/v1 |
| `openrouter` | openrouter.ai/api/v1 |
| `ollama-local` | localhost:11434/v1 |
| `lmstudio` | localhost:1234/v1 |

For services without built-in support, use `openai-compat` with a custom `base_url`:

```toml
# Together AI
[llm]
provider = "openai-compat"
model = "meta-llama/Llama-3-70b-chat-hf"
max_tokens = 4096
base_url = "https://api.together.xyz/v1"

# Fireworks AI
[llm]
provider = "openai-compat"
model = "accounts/fireworks/models/llama-v3-70b-instruct"
max_tokens = 4096
base_url = "https://api.fireworks.ai/inference/v1"

# Anyscale
[llm]
provider = "openai-compat"
model = "meta-llama/Llama-3-70b-chat-hf"
max_tokens = 4096
base_url = "https://api.endpoints.anyscale.com/v1"

# Perplexity
[llm]
provider = "openai-compat"
model = "llama-3.1-sonar-large-128k-online"
max_tokens = 4096
base_url = "https://api.perplexity.ai"

# DeepSeek
[llm]
provider = "openai-compat"
model = "deepseek-chat"
max_tokens = 4096
base_url = "https://api.deepseek.com/v1"

# vLLM self-hosted
[llm]
provider = "openai-compat"
model = "meta-llama/Llama-3-70b"
max_tokens = 4096
base_url = "http://localhost:8000/v1"

# LiteLLM proxy
[llm]
provider = "openai-compat"
model = "gpt-4"
max_tokens = 4096
base_url = "http://localhost:4000"
```

### Provider Examples

```toml
# xAI (Grok)
[llm]
provider = "xai"
model = "grok-2"
max_tokens = 4096

# OpenRouter - route to any model
[llm]
provider = "openrouter"
model = "anthropic/claude-3.5-sonnet"
max_tokens = 4096

# Local Ollama
[llm]
provider = "ollama-local"
model = "llama3:70b"
max_tokens = 4096

# LMStudio local server
[llm]
provider = "lmstudio"
model = "loaded-model"
max_tokens = 4096
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

The `provider` field is **optional** for standard models — the agent automatically determines the provider using pattern inference from model name prefixes:
   - `claude-*` → anthropic
   - `gpt-*`, `o1-*`, `o3-*` → openai
   - `gemini-*`, `gemma-*` → google
   - `mistral-*`, `mixtral-*`, `codestral-*` → mistral
   - `grok-*` → xai

Set `provider` explicitly for custom or ambiguous model names.

### Required Configuration

| Field | Description |
|-------|-------------|
| `model` | Model identifier (e.g., "claude-sonnet-4-20250514") |
| `max_tokens` | Maximum tokens for response generation |

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

### XML Context Format

The agent uses XML-structured prompts to communicate context between goals. This provides clear boundaries that don't collide with LLM-generated markdown:

```xml
<workflow name="recipe-creator">

<context>
  <goal id="brainstorm">
Here are 3 distinct dishes:

## 1. Classic South Indian Coconut Chutney
A fresh, vibrant accompaniment...
  </goal>
</context>

<current-goal id="select">
Choose the best recipe based on: ingredient usage, flavor balance.
</current-goal>

</workflow>
```

Benefits:
- **Clear boundaries** — XML tags separate system context from LLM output
- **No header collisions** — LLM's `##` headers don't conflict with context headers
- **Machine-parseable** — Tools can extract specific goals from logs
- **Session logs** — The XML structure appears in session logs for debugging

See [docs/execution/06-xml-context-format.md](docs/execution/06-xml-context-format.md) for full documentation.

