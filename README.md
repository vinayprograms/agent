# Headless Agent

A Go-based headless agent for workflow automation using LLMs.

> **Disclaimer:** This code was **gramcoded** — 12 days of texting an AI via Telegram, whenever inspiration hit. No IDE, no terminal — just a phone and [OpenClaw](https://github.com/openclaw/openclaw) + Claude Opus 4.5. **Review before production use**.

## Quick Start

### Option A: Interactive Setup (Recommended)

```bash
make build
./bin/agent setup
```

The setup wizard guides you through configuration for any deployment scenario.

### Option B: Manual Setup

```bash
# Build
make build          # Build to ./bin/agent
make install        # Install to ~/.local/bin/agent

# Create agent.toml
cat > agent.toml <<'EOF'
[agent]
id = "my-agent"
workspace = "/path/to/your/workspace"

[llm]
model = "claude-sonnet-4-20250514"
max_tokens = 4096
thinking = "auto"
EOF

# Set API key
export ANTHROPIC_API_KEY="your-key-here"

# Create an Agentfile
cat > Agentfile <<'EOF'
NAME hello-world
INPUT topic DEFAULT "Go programming"
GOAL research "Research $topic and list 3 key facts"
GOAL summarize "Summarize the research in one sentence"
RUN main USING research, summarize
EOF

# Run
./bin/agent run --config agent.toml
```

The `provider` field is optional — it is inferred from the model name (`claude-*` -> Anthropic, `gpt-*` -> OpenAI, `gemini-*` -> Google, etc.). See [LLM Providers](docs/configuration/llm-providers.md) for details.

## Features

| Feature | Description | Docs |
|---------|-------------|------|
| **Multi-provider LLM** | Anthropic, OpenAI, Google, Mistral, Groq, xAI, Ollama, and more | [LLM Providers](docs/configuration/llm-providers.md) |
| **Capability Profiles** | Route agents to different models by declared intent | [Profiles](docs/configuration/profiles.md) |
| **Adaptive Thinking** | Per-request reasoning depth via heuristic classifier | [Thinking](docs/configuration/thinking.md) |
| **Semantic Memory** | Persistent BM25 + semantic graph memory across sessions | [Memory](docs/memory/semantic-memory.md) |
| **Security Framework** | Trust-tagged blocks, tiered verification, audit trail | [Security](docs/security/README.md) |
| **Supervision** | Four-phase execution with drift detection and human approval | [Execution](docs/execution/README.md) |
| **Packaging** | Signed, distributable agent packages | [Packaging](docs/usage/packaging.md) |
| **MCP / ACP** | External tool servers and editor integration | [Protocols](docs/configuration/protocols.md) |
| **Web Search** | SearXNG, Brave, Tavily, DuckDuckGo with auto-fallback | [Web Search](docs/configuration/web-search.md) |
| **Sub-Agents** | Static (AGENT/USING) and dynamic (spawn_agent) sub-agents | [Design](docs/design/05-subagents.md) |
| **Agent Skills** | Load reusable skills from SKILL.md directories | [Protocols](docs/configuration/protocols.md) |
| **Docker** | CGO-free builds for minimal container images | [Docker](docs/usage/docker.md) |

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
| `agent serve` | Run as A2A/ACP server |

See [CLI Reference](docs/usage/cli-reference.md) for flags and Makefile targets.

## Agentfile Syntax

```
NAME workflow-name
INPUT required_param
INPUT optional_param DEFAULT "value"

SUPERVISED                                          # Global supervision
SECURITY default                                    # or: paranoid, research "scope"

AGENT researcher FROM agents/researcher.md -> findings, sources
AGENT critic FROM skills/code-review REQUIRES "fast"

GOAL analyze "Analyze $topic" -> summary USING researcher, critic
GOAL deploy "Deploy $service" SUPERVISED HUMAN

RUN pipeline USING analyze, deploy
LOOP refine USING improve WITHIN 5
```

See [Agentfile DSL](docs/design/02-agentfile.md) for full syntax reference.

## Built-in Tools

**File Operations:** `read`, `write`, `edit`, `glob`, `grep`, `ls`
**Shell:** `bash` (requires policy `[bash] enabled = true`)
**Web:** `web_search`, `web_fetch`
**Memory:** `memory_read`, `memory_write`, `memory_list`, `memory_search`, `remember`, `recall`, `memory_forget`
**Agents:** `spawn_agent`, `spawn_agents`

## API Keys

Keys are loaded in priority order:

1. Environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.)
2. `.env` file in current directory
3. `~/.config/grid/credentials.toml`

**Never commit credentials to git.** Add `credentials.toml`, `.env`, and `*.pem` to your `.gitignore`.

## Security

Create `policy.toml` to restrict tool access:

```toml
default_deny = true
workspace = "/path/to/workspace"

[tools.read]
enabled = true
allow = ["$WORKSPACE/**"]
deny = ["**/.env", "**/*.key"]

[tools.bash]
enabled = true
denylist = ["rm *", "sudo *"]
```

See [Security docs](docs/security/README.md) for the full framework (trust boundaries, tiered verification, audit trail, research mode).

## Documentation

- **Design:** [Architecture](docs/design/01-architecture.md) | [Agentfile DSL](docs/design/02-agentfile.md) | [LLM](docs/design/03-llm.md) | [Tools](docs/design/04-tools.md) | [Sub-Agents](docs/design/05-subagents.md) | [Packaging](docs/design/06-packaging.md)
- **Configuration:** [LLM Providers](docs/configuration/llm-providers.md) | [Profiles](docs/configuration/profiles.md) | [Thinking](docs/configuration/thinking.md) | [Web Search](docs/configuration/web-search.md) | [Protocols](docs/configuration/protocols.md)
- **Usage:** [CLI Reference](docs/usage/cli-reference.md) | [Packaging](docs/usage/packaging.md) | [Docker](docs/usage/docker.md)
- **Execution:** [Four-Phase Execution](docs/execution/01-four-phase-execution.md) | [Supervision](docs/execution/03-supervision-modes.md)
- **Security:** [Threat Model](docs/security/01-threat-model.md) | [Trust Boundaries](docs/security/02-trust-boundaries.md) | [Security Modes](docs/security/07-security-modes.md)
- **Memory:** [Semantic Memory](docs/memory/semantic-memory.md)

## Examples

See `examples/agent/` for 44+ single-agent workflows and `examples/swarm/` for 15 multi-agent swarm examples.

## License

See [LICENSE](LICENSE) for details.
