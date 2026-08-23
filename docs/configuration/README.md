# Configuration Guide

This guide covers the three files that configure the agent, where each is
looked up, how to get a working setup in minutes, and how to manage several
configurations side by side. It is the reference for a developer setting up
the agent for the first time and for anyone managing multiple deployments
afterward.

## 1. The three files

| File | Owns |
|------|------|
| `agent.toml` | Models: `[llm]`, `[small_llm]`, `[profiles.*]`; execution behavior: `[limits]`, `[timeouts]`; and integration: `[mcp.servers.*]`, `[skills]`, `[state]`, `[telemetry]`, `[security]` mode, `[service]` (for `agent serve`). |
| `policy.toml` | What tools may do: `default_deny`, `allowed_dirs`, per-tool `[tools.<name>]` allow/deny patterns, `[mcp]` enabled/allow, `[content.security]` extra patterns and keywords. |
| `credentials.toml` | API keys and OAuth tokens, one `[<provider>]` table per provider. Must be `0400` or `0600`; never commit it. |

`agent.toml` decides *what the agent can be* (which models, which tools are
wired up). `policy.toml` decides *what those tools are allowed to touch*.
`credentials.toml` supplies the secrets both need. All three are TOML and
all three are optional — the agent runs with built-in defaults, a
permissive policy, and credentials from the environment if none are found.

## 2. Where they are looked up

Each file has its own precedence order (lowest to highest), independent of
the other two.

**`agent.toml`** — layered; every file found is merged, later layers
override earlier ones:

| Order | Source |
|-------|--------|
| 1 (lowest) | `~/.config/agent/agent.toml` |
| 2 | `./agent.toml` (project directory) |
| 3 | `$AGENT_CONFIG` env var, if set (path to a TOML file) |
| 4 (highest) | `--config <path>` |

**`policy.toml`** — first found wins, nothing is merged:

| Order | Source |
|-------|--------|
| 1 (highest) | `--policy <path>` (must exist and parse) |
| 2 | `<Agentfile dir>/policy.toml` |
| 3 | `~/.config/agent/policy.toml` |
| 4 (lowest) | none found → permissive default (every tool enabled), with a warning naming both paths checked |

**`credentials.toml`** — composed by provider, in increasing priority
(`env < file < Claude CLI`); within the file layer, first found wins:

| Order | Source |
|-------|--------|
| 1 (lowest) | Environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, ...) |
| 2 | First existing file among: `--credentials <path>` (must exist), else `./credentials.toml`, else `~/.config/agent/credentials.toml`, else `~/.agent/credentials.toml` |
| 3 (highest) | Claude CLI OAuth token at `~/.claude/.credentials.json`, for the `anthropic` provider only |

`.env` in the current directory is loaded before all of the above (via
`godotenv`), so it effectively seeds the environment layer.

`agent config path` prints all three precedence chains against your actual
filesystem, marking each candidate `in effect`, `shadowed`, or `missing`.

## 3. Quick start

### Non-interactive: `agent config init`

Five commands to a working run with Anthropic:

```bash
# 1. Build (or install) the binary
go build -o agent ./cmd/agent

# 2. Generate agent.toml + policy.toml for the project directory
./agent config init --provider anthropic --model claude-sonnet-4-20250514 \
  --api-key-env ANTHROPIC_API_KEY

# 3. Export the key the config points at
export ANTHROPIC_API_KEY=sk-ant-...

# 4. Sanity-check the result
./agent config validate

# 5. Run
./agent run --goal "say hello"
```

`agent config init --default` writes to `~/.config/agent/` instead, so every
project on the machine inherits it. `--api-key <key>` stores the key in
`credentials.toml` (mode `0600`) rather than reading it from the
environment. `--scenario` (`local`, `dev` — default, `team`, `production`,
`docker`) presets the security stance, provider and feature flags together;
see `agent config init --help` for the full flag list.

### Interactive: `agent setup`

```bash
./agent setup
```

Walks through the same questions (provider, model, security mode, MCP
servers, telemetry) and writes the same two files, pre-filled from an
`agent.toml` already in the target directory if one exists. `--dir` or
`--default` picks the target the same way `config init` does. Any API key
entered is always written to `~/.config/agent/credentials.toml`, regardless
of target — a key belongs to the user, not to one project.

## 4. Managing multiple configurations

Every `agent config` subcommand and `agent setup` take the same **target**:

| Target | Meaning |
|--------|---------|
| *(neither flag)* | Current directory, with the normal lookup precedence |
| `--dir <path>` | Exactly that directory |
| `--default` | The user-level directory, `~/.config/agent/` |

Use this to keep separate configuration sets — say, a cheap local Ollama
setup for day-to-day work and a paranoid production setup for a deployed
agent — as sibling directories:

```bash
./agent config init --dir ~/agents/local --provider ollama-local --scenario local
./agent config init --dir ~/agents/prod  --provider anthropic --scenario production --api-key-env ANTHROPIC_API_KEY
```

Run against one set explicitly by pointing all three flags at it:

```bash
./agent run --config ~/agents/prod/agent.toml \
             --policy ~/agents/prod/policy.toml \
             --credentials ~/agents/prod/credentials.toml \
             my-workflow.agent
```

Omitting a flag falls back to that file's own precedence chain (section 2),
so you can mix an explicit `--config` with the default policy lookup, etc.

Inspect and check a set with the same target flags:

```bash
./agent config path --dir ~/agents/prod       # what would be loaded, and from where
./agent config show --dir ~/agents/prod       # print the files in effect (secrets redacted)
./agent config show --dir ~/agents/prod --resolved   # merged agent.toml as TOML
./agent config validate --dir ~/agents/prod   # parse everything, report problems
```

`agent config show` redacts API keys and tokens (`api_key = "sk-…abcd"`).
`agent config validate` uses the same loaders as `agent run`, so a clean
`validate` means the run will not fail on configuration.

## 5. `agent.toml` reference

Fields not shown default to the zero value (`""`, `0`, `false`, empty
map/slice) unless noted. This mirrors `internal/config/config.go`.

```toml
[agent]
# id = "my-agent"        # optional identifier
workspace = "."           # default: current working directory

[llm]
provider = "anthropic"          # anthropic|openai|google|groq|mistral|xai|
                                 # openrouter|ollama-cloud|ollama-local|litellm|lmstudio
model = "claude-sonnet-4-20250514"
max_tokens = 4096                # default: 4096
api_key_env = "ANTHROPIC_API_KEY"
# base_url = "https://..."       # OpenRouter, LiteLLM, Ollama, LMStudio
thinking = "auto"                # auto|off|low|medium|high
# max_retries = 5                # default: 5
# retry_backoff = "60s"          # default: "60s"

[small_llm]
# Same fields as [llm]; a fast/cheap model for summarization and triage.
provider = "anthropic"
model = "claude-3-5-haiku-20241022"
max_tokens = 1024

[profiles.reasoning]
# Capability profile selected by an Agentfile's `REQUIRES "reasoning"`.
# Unset fields (provider, api_key_env, max_tokens) inherit from [llm].
model = "claude-opus-4-20250514"
thinking = "high"

[limits]
# Per-goal execution budget. 0 (the default) means unlimited.
max_tool_calls = 40      # tool calls per goal
max_turns = 25           # LLM turns per goal
max_duration = "10m"     # Go duration string; unparseable values fail to load

[security]
mode = "default"          # default|paranoid
# user_trust = "trusted"  # trusted|vetted|untrusted
# triage_llm = "fast"     # profile name for Tier 2 triage

[mcp.servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
# env = { API_KEY = "..." }
# denied_tools = ["delete_file"]   # hidden from the LLM even if policy.toml allows the server

[skills]
paths = ["./skills", "~/.agent/skills"]

[state]
# Base directory for persistent data (BM25 memory). Default: ~/.local/agent
location = "~/.local/agent"

[telemetry]
enabled = false
protocol = "noop"         # noop|grpc|http; default: noop
# endpoint = "localhost:4317"
insecure = false
# [telemetry.headers]
# DD-API-KEY = "..."

[timeouts]
mcp = 60                  # seconds; default: 60
web_search = 30           # seconds; default: 30
web_fetch = 60            # seconds; default: 60
search_cooldown_ms = 2000 # ms between DuckDuckGo queries; default: 2000

[embedding]
# Provider for resume vectors: openai|google|openai-compat|litellm|none
provider = "none"
# model = "text-embedding-3-small"
# api_key = "..."          # or rely on credentials.toml
# base_url = "http://localhost:11434/v1"

[service]
# Settings for `agent serve` only.
# bus_url = "nats://localhost:4222"   # swarm mode; empty = local HTTP mode
# http_addr = ":8080"
# queue_group = "my-capability"
# heartbeat_interval = "5s"
# drain_timeout = "30s"
# capability = "my-capability"
```

`[web]` (`gateway_url`, `gateway_token_env`) configures an Internet Gateway
proxy for `web_search`/`web_fetch`. `[web] search_provider` pins `web_search`
to one backend (`""`/`"auto"`, `"searxng"`, `"brave"`, `"tavily"`, or
`"duckduckgo"`; any other value is a config-load error naming the bad value),
and `[web] searxng_url` sets the SearXNG instance URL, taking precedence over
the `[searxng]` credential and the `SEARXNG_URL` env var. See the
[Web Search](web-search.md) page for provider fallback order and env vars.

`[profiles.*]`, `[mcp.servers.*]` and `REQUIRES` are covered in depth in
[Capability Profiles](profiles.md) and [Protocols](protocols.md).
`thinking` levels are covered in [Adaptive Thinking](thinking.md).
`[limits]` is covered fully in [Goal Limits](limits.md). Provider values and
their conventional `api_key_env` names are listed in
[LLM Providers](llm-providers.md).

### Deprecated: `[storage]`

`[storage]` (`path` or `location`) is the pre-migration name for `[state]`.
It still loads — the value becomes `cfg.State.Location` — but every load
emits a warning:

```
WARN: [storage] is deprecated, rename to [state] with location = "./storage"
```

Having **both** `[state]` and `[storage]` in the same file is a hard error:

```
config has both [state] and [storage]; remove deprecated [storage] section
```

## 6. `policy.toml` reference

```toml
default_deny = true
# true:  tools without a [tools.<name>] table are disabled.
# false: every tool is enabled by default; [tools.*] tables only restrict it.

allowed_dirs = ["$WORKSPACE", "/tmp"]
# Universal filesystem boundary for every path-touching tool (read, write,
# edit, bash, glob, grep, ls). $WORKSPACE expands to [agent].workspace; ~
# expands to the user's home. The workspace is always added automatically
# even if allowed_dirs is omitted or doesn't mention it.

[tools.read]
allow = ["$WORKSPACE/**"]
deny = ["**/.env", "**/*.key", "**/credentials.toml"]   # deny wins over allow

[tools.write]
deny = ["agent.toml", "policy.toml", "credentials.toml"]

[tools.bash]
# Presence of this table is what turns bash on. deny holds bare command
# names added to shellguard's built-in banned-command list (see
# docs/security/10-bash-security.md); there is no allowlist or sandbox.
deny = ["docker", "kubectl"]

[tools.web_fetch]
allow = ["github.com", "*.github.io", "docs.python.org"]

[mcp]
enabled = true
allow = ["filesystem:read_file", "memory:*"]   # "server:tool", "*" wildcards

[content.security]
patterns = ["exfil_curl:(?i)curl\\s+.*(-d|--data)"]   # "name:regex"
keywords = ["confidential"]                            # case-insensitive
```

A tool is enabled purely by having a `[tools.<name>]` table (or by
`default_deny = false`) — there is no per-tool `enabled` key. `allow`/`deny`
patterns are interpreted by the tool (path globs for filesystem tools,
domain patterns for web tools, bare command names for bash). `deny` always
wins over `allow`. `sandbox` and `timeout` keys under `[tools.bash]` parse
but are not enforced by this agent.

Protected files (`agent.toml`, `policy.toml`, `credentials.toml`) can never
be modified via `write`/`edit`, regardless of policy.

See [Security Modes](../security/07-security-modes.md) for `[security]`
mode/user_trust semantics and [Bash Security](../security/10-bash-security.md)
for the full shellguard model.

### Legacy key migration

agentkit v1.2.0 removed several pre-migration keys. Any of them in a
`policy.toml` fails validation with every offending key named and its
replacement:

```
policy.toml uses keys the current policy schema does not recognise:
  enabled -> list the tool under [tools.<name>]
  denylist -> deny
```

| Old key | New key / replacement |
|---|---|
| `enabled` | list the tool under `[tools.<name>]` |
| `allowlist` | (removed; use `deny` + LLM review) |
| `denylist` | `deny` |
| `allow_domains` | `allow` |
| `rate_limit` | (removed) |
| `memory_read.enabled` | list `recall` under `[tools.recall]` |
| `memory_write.enabled` | list `remember` under `[tools.remember]` |
| `mcp.default_deny` | `mcp.enabled` |
| `mcp.allowed_tools` | `mcp.allow` |
| `security.extra_patterns` | `content.security.patterns` |
| `security.extra_keywords` | `content.security.keywords` |

Any other unrecognized key is reported as "unknown key (remove it)".

## 7. `credentials.toml` reference

```toml
[anthropic]
api_key = "sk-ant-..."

[openai]
api_key = "sk-..."

[google]
api_key = "AIza..."

[google.oauth]
access_token = "ya29.a0..."
refresh_token = "1//0e..."
expires_at = 2024-06-01T00:00:00Z
scopes = ["https://www.googleapis.com/auth/cloud-platform"]
refresh_url = "https://oauth2.googleapis.com/token"
```

One `[<provider>]` table per provider, each with `api_key` and/or an
`[<provider>.oauth]` sub-table. When both are present, a valid (unexpired)
OAuth token is preferred over the API key.

**Environment variable fallback** — `credentials.toml` is optional because
each provider also has a conventional env var:

| Provider | Env var |
|---|---|
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai` / `openai-compat` | `OPENAI_API_KEY` |
| `google` | `GOOGLE_API_KEY` |
| `mistral` | `MISTRAL_API_KEY` |
| `groq` | `GROQ_API_KEY` |
| `brave` | `BRAVE_API_KEY` |
| `tavily` | `TAVILY_API_KEY` |

**Claude CLI / OAuth** — if `anthropic` has no key anywhere else and
`~/.claude/.credentials.json` (written by the Claude Code CLI's own login)
has a usable OAuth token, the agent uses it automatically; this is the
highest-priority source. `agent setup` detects this and offers "Use Claude
CLI credentials (already authenticated)" as a credential method so you can
skip both the API key and the env var.

**Hygiene**

- The file must be mode `0400` or `0600`; anything looser fails to load with `credentials file has insecure permissions: <mode>`.
- `agent config init --api-key` and `agent setup` always write it at `0600`.
- Never commit it. This repo's `.gitignore` already excludes `credentials.toml`, `.env`, and `*.pem` — carry the same entries into any project you configure.
- `agent config show` redacts every key it prints.

## 8. Troubleshooting

**`warning: no policy file at <project>/policy.toml or <home>/.config/agent/policy.toml; all tools enabled`**
No `policy.toml` was found anywhere in the chain. The run proceeds with
every tool enabled (the pre-migration default), which is fine for local
experimentation but should not reach production. Run `agent config init`
or `agent setup` to generate one, or pass `--policy <path>`.

**`config has both [state] and [storage]; remove deprecated [storage] section`**
Delete the `[storage]` table and keep only `[state]`
(`location = "..."` replaces `path`/`location` under `[storage]`).

**`WARN: [storage] is deprecated, rename to [state] with location = "..."`**
Non-fatal; the value still loads. Rename the section when convenient.

**`<file> uses keys the current policy schema does not recognise: ...`**
One or more legacy policy keys are present. See the migration table in
section 6 and rename each key listed.

**A provider is configured but no credential is found**
`agent config validate` reports this as a *warning*, not a failure, since
the key may arrive from the environment at run time:
"a configured provider with no credential anywhere is reported as a
warning". `ollama-local` and `lmstudio` are exempt (no key needed). Fix by
setting the provider's env var, adding it to `credentials.toml`, or
confirming the Claude CLI is logged in for `anthropic`.

**`[limits] max_duration "<value>": <parse error>`**
`max_duration` must be a Go duration string (`"90s"`, `"10m"`, `"1h30m"`).
Fix the value in `agent.toml`; an unparseable value fails the whole config
load rather than silently being treated as unlimited.

**`workspace conflict: --workspace="..." resolves to "...", agent.toml has "..."`**
`--workspace` and `[agent].workspace` in the loaded config resolve to
different absolute paths. Drop one of them, or make them agree.

**`--step needs an interactive terminal`**
`agent run --step` pauses after each goal and asks whether to continue; it
requires an interactive stdin and stderr. Drop `--step` for non-interactive
runs (CI, scripts, `agent serve`).

**`credentials file has insecure permissions: <mode>`**
`chmod 0600 credentials.toml` (or `0400` for a read-only copy).

**`<path> already exists (use --force to overwrite)`**
`agent config init` never overwrites without `--force`; either pass it or
target a fresh `--dir`.
