# CLI Reference

## Commands

| Command | Description |
|---------|-------------|
| `agent run <file>` | Execute a workflow |
| `agent validate <file>` | Check syntax without running |
| `agent inspect <file>` | Show workflow/package structure |
| `agent pack <dir>` | Create a signed package |
| `agent verify <pkg>` | Verify package signature |
| `agent install <pkg>` | Install a package |
| `agent keygen` | Generate signing key pair |
| `agent config` | Create and inspect configuration files (see below) |
| `agent setup` | Interactive setup wizard |
| `agent serve` | Run as A2A/ACP server |
| `agent replay [target]...` | List and replay recorded sessions (see below) |
| `agent help` | Show help |
| `agent version` | Show version |

## Flags

| Flag | Description |
|------|-------------|
| `--config <path>` | Config file (default: `agent.toml`) |
| `--input key=value` | Provide input (repeatable) |
| `-f <path>` | Specify Agentfile path |
| `--policy <path>` | Security policy file |
| `--credentials <path>` | Credentials file (`agent run` and `agent serve`); highest precedence, must exist and parse |
| `--workspace <path>` | Override workspace directory |
| `--session-label <label>` | `agent run` and `agent serve`: label recorded in the session header, identifying the deployment that produced the run. `agent replay --label` selects on it. |
| `--step` | `agent run` only: pause after each goal and ask whether to continue. Needs an interactive terminal (stdin and stderr); the run is refused otherwise. Answer Enter/`y` to continue or `n` to stop — stopping ends the run with "aborted" status and a non-zero exit. |

## Session Replay

`agent replay` reads the sessions recorded under `<state>/sessions` — one
`<session-id>.jsonl` file per run. The state location comes from `[state]
location` in the config (`--config <path>` to pick the file, `--state
<dir>` to override it outright).

With no arguments it lists the recorded sessions:

```
$ agent replay
ID                                CREATED              NAME   LABEL  STATUS    DURATION
6f0e...                           2026-08-22 10:14:07  hello  -      complete  42s
```

Arguments say what to replay: a session id, a unique id prefix, a session
file, or a directory to glob for session files. An ambiguous prefix prints
the candidates and exits non-zero.

| Flag | Description |
|------|-------------|
| `--list` | Print the matching sessions as a table instead of replaying them |
| `--last` | Select the most recently created session |
| `--name <name>` | Select sessions by workflow NAME |
| `--agentfile <path>` | Select sessions by Agentfile path or file name |
| `--label <label>` | Select sessions by deployment label (`agent serve --session-label`) |
| `--status <status>` | Select sessions by status: `running`, `complete`, `failed`, `aborted` |
| `--since <duration>` | Select sessions created within a Go duration, e.g. `2h` |
| `--state <dir>` | Override the state location |
| `--config <path>` | Config file to read `[state] location` from |
| `-v`, `-vv` | Verbosity: tool arguments, then full prompts and responses |
| `-f`, `--follow` | Watch a single session file and reload as it grows |
| `--no-pager` | Write to stdout instead of the interactive pager |
| `--cost <model:in,out>` | Model pricing per 1M tokens, repeatable |

When a selection matches several sessions they replay in creation order.
The standalone `agent-replay` binary has the same surface.

```bash
agent replay --last                  # replay the most recent run
agent replay a1b2c3d4                # replay by id prefix
agent replay --name hello --list     # list one workflow's sessions
agent replay --status failed --since 24h
```

## Configuration Commands

See the [Configuration Guide](../configuration/README.md) for the full reference on
`agent.toml`, `policy.toml` and `credentials.toml` — every field, precedence, quick
start and troubleshooting.

`agent config` is the non-interactive counterpart of `agent setup`. Both work on the
same three files — `agent.toml` (models and features), `policy.toml` (what tools may
do) and `credentials.toml` (API keys) — and take the same **target**:

| Target | Meaning |
|--------|---------|
| *(neither flag)* | The current directory, with the normal lookup precedence applied |
| `--dir <path>` | Exactly that directory |
| `--default` | The user-level directory, `~/.config/agent/` |

`--dir` and `--default` are mutually exclusive.

| Command | Description |
|---------|-------------|
| `agent config init` | Write a starter `agent.toml` and `policy.toml` |
| `agent config show` | Print the files in effect, headed by where each came from |
| `agent config validate` | Check every file with the loaders `agent run` uses |
| `agent config path` | List every path consulted, in precedence order |

### `agent config init`

```
agent config init [--dir PATH | --default] [--provider P] [--model M]
                  [--small-model M] [--scenario S] [--api-key K | --api-key-env VAR]
                  [--force]
```

| Flag | Description |
|------|-------------|
| `--provider` | `anthropic`, `openai`, `google`, `groq`, `mistral`, `xai`, `openrouter`, `ollama-cloud`, `ollama-local`, `litellm`, `lmstudio`, `custom` |
| `--model` | Main model name (default: the provider's recommended model) |
| `--small-model` | Fast model for summarization and triage |
| `--scenario` | `local`, `dev` (default), `team`, `production`, `docker` — presets for security stance, provider and features |
| `--api-key` | Store the key in a `credentials.toml` (mode 0600) beside the other files |
| `--api-key-env` | Record the environment variable to read the key from; writes no credentials file |
| `--force` | Overwrite existing files |

`agent.toml` and `policy.toml` are written mode 0644, `credentials.toml` mode 0600.
Without `--force`, an existing file blocks the whole write and nothing is changed.
The paths written are printed.

```bash
agent config init --provider anthropic --model claude-sonnet-4-20250514
agent config init --default --scenario production --api-key-env ANTHROPIC_API_KEY
```

### `agent config show`

Prints each file that is in effect, headed by the path it came from. When the target
is the current directory, `agent.toml` layers, so the header names every source:

```
agent.toml: ~/.config/agent/agent.toml + agent.toml (merged)
```

API keys and tokens are redacted (`api_key = "sk-…abcd"`). `--resolved` prints the
merged configuration as TOML instead of the files themselves.

### `agent config validate`

Parses every file in effect with the same loaders `agent run` uses and reports all
problems at once, exiting non-zero if there are any: malformed TOML, deprecated
sections, legacy policy keys (each with its replacement named), and a credentials
file with insecure permissions.

A provider configured in `agent.toml` with no credential in the file, the
environment or the Claude CLI is reported as a **warning**, not a failure — the key
may legitimately arrive from the environment at run time. Local providers
(`ollama-local`, `lmstudio`) are exempt.

### `agent config path`

Lists every candidate path per file in precedence order, marked `in effect`,
`shadowed` (it exists but a higher-precedence file won) or `missing`.

| File | Lookup order |
|------|--------------|
| `agent.toml` | `~/.config/agent/agent.toml`, then `./agent.toml`, then `$AGENT_CONFIG`, then `--config` — **merged**, later layers override |
| `policy.toml` | `--policy`, then `<Agentfile dir>/policy.toml`, then `~/.config/agent/policy.toml`, else permissive — **first found wins** |
| `credentials.toml` | `--credentials`, then `./credentials.toml`, then `~/.config/agent/credentials.toml`, then `~/.agent/credentials.toml` — **first found wins** |

### `agent setup`

```
agent setup [--dir PATH | --default]
```

The interactive wizard takes the same target flags. It pre-fills its answers from an
`agent.toml` already in the target directory and writes `agent.toml` and `policy.toml`
back there, overwriting them. The API key, if one is entered, is always written to
`~/.config/agent/credentials.toml` regardless of the target — a key belongs to the
user, not to a project.

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

## Quick Validation

```bash
# Validate an Agentfile (uses ./Agentfile by default)
./agent validate

# Validate a specific file
./agent validate -f path/to/MyAgentfile

# Run with custom input
./agent run --config agent.toml --input topic="Rust programming"

# Run a specific Agentfile
./agent run -f examples/hello.agent --config agent.toml
```

---

Back to [README](../../README.md) | See also: [Packaging](packaging.md), [Docker](docker.md)
