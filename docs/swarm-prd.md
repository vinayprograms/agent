# swarm — Personal Swarm Controller

## 1. Overview

`swarm` is a CLI/TUI tool for managing small agent swarms on a single host. It provides a human interface to a set of headless agents connected via NATS — submitting tasks, monitoring agents, viewing results, and replaying sessions.

Think `docker compose` for agent swarms. Start small, experiment locally, graduate to Hive when complexity demands it.

## 2. Problem

Running `agent serve --bus` gives you headless agents listening on NATS. But there's no unified way to:

- See which agents are running and what they can do
- Submit work and track results
- Debug failures across multiple agents
- Start/stop a local swarm from a manifest

Users resort to raw NATS publishes, curl, or custom scripts.

## 3. Users

Solo developer or small team running a personal swarm (2–10 agents) on a single machine or home lab. Not enterprise orchestration — "docker compose" scale, not "kubernetes" scale.

## 4. Non-Goals

- **Not a scheduler/orchestrator** — that's Hive's dispatcher (DAGs, conditional routing, retry policies)
- **Not a deployment tool for remote hosts** — agents run locally
- **Not multi-tenant** — single user, single NATS
- **Not a replacement for Hive** — it's the human interface TO a local swarm

### Boundary with Hive

`swarm` supports simple linear task chaining (`A → B → C`). The moment you need branching, conditionals, parallel fan-out, or DAGs, that's Hive. If you need to describe the workflow in a file, you've outgrown swarm.

## 5. Ecosystem Context

| Binary | Purpose |
|--------|---------|
| `agent` | Run one agent (`agent run`, `agent serve`) |
| `replay` | View one session log |
| `agentmem` | Inspect agent memory |
| `swarm` | Manage many agents |

`swarm` is the multi-agent counterpart to `agent`. It consumes the same NATS subjects, TaskMessage/TaskResult types, and heartbeat protocol defined in agentkit.

## 6. Architecture

```
swarm ──→ NATS ──→ agent serve (×N)
      ←── NATS ←── (heartbeats, done.*)
```

`swarm` is a pure NATS client. No sidecar, no API server. It discovers agents via heartbeat messages, submits tasks using `work.<capability>.<task_id>`, and listens for results on `done.<capability>.<task_id>`.

**Zero new infrastructure.** Everything it needs already exists in agentkit:

- Agent discovery → heartbeat messages (`agentkit/heartbeat`)
- Task submission → `TaskMessage` (`agentkit/tasks`)
- Results → `TaskResult` on `done.*` subjects
- Health → heartbeat status/load fields

### NATS Dependency

`swarm` requires an external NATS server (not embedded). Embedding would make the CLI a server — if it exits, all agents lose their bus.

`swarm up` checks for NATS availability:
- If NATS is running → connect
- If not → attempt to start `nats-server` as a background process
- If `nats-server` not installed → error with installation instructions

## 7. Manifest Format

Manifests use YAML. The manifest is a deployment descriptor ("what to run"), not application configuration ("how to behave"). Agent.toml/policy.toml remain TOML.

```yaml
# swarm.yaml
nats:
  url: nats://localhost:4222

agents:
  - name: coder
    agentfile: ./agents/coder.agent
    config: ./agents/agent.toml
    policy: ./agents/policy.toml
    capability: coder

  - name: tester
    agentfile: ./agents/tester.agent
    config: ./agents/agent.toml
    policy: ./agents/policy.toml
    capability: tester

  - name: documenter
    agentfile: ./agents/documenter.agent
    config: ./agents/agent.toml
    capability: documenter
```

### Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Display name for the agent instance |
| `agentfile` | Yes | Path to Agentfile |
| `config` | No | Path to agent.toml (uses agent defaults if omitted) |
| `policy` | No | Path to policy.toml |
| `capability` | No | Capability name override (defaults to Agentfile NAME) |

## 8. CLI Commands

### Swarm Lifecycle

```
swarm up [agent...]              # Start swarm (or specific agents) from swarm.yaml
swarm down [agent...]            # Graceful shutdown (or specific agents)
swarm restart [agent...]         # Restart agents
swarm status                     # Overview: NATS connection, agents, capabilities
```

### Agent Management (Observe)

```
swarm agents                     # List agents with capability, status, load
swarm capabilities               # List available capabilities across swarm
```

### Task Management

```
swarm submit <capability> "<task>"   # Submit work, returns task_id
swarm submit <cap> -f input.json     # Submit with structured inputs
swarm result <task_id>               # Fetch result (poll or --wait)
swarm history                        # Recent tasks with status/duration
swarm history --capability coder     # Filter by capability
swarm history --status failed        # Filter by status
```

### Task Chaining (Simple Linear Pipes)

```
swarm chain <cap1> "<task>" | <cap2> | <cap3>
```

Output of each stage becomes input for the next. Linear only — no branching, no conditionals. For anything more complex, use Hive.

### Replay

```
swarm replay <task_id>           # TUI timeline view
swarm replay <task_id> --web     # Generate HTML, open in browser
```

### Interactive TUI

```
swarm ui                         # Full TUI dashboard
```

## 9. TUI Dashboard (`swarm ui`)

Built with bubbletea.

### Layout

```
┌─ Agents ──────────┬─ Tasks ─────────────────────────────┐
│ coder     idle   0%│ abc123  coder   ✓ success    2.3s  │
│ tester    busy  80%│ def456  tester  ⏳ running    ...   │
│ documenter idle  0%│ ghi789  coder   ✗ failed     0.8s  │
│                    │                                     │
├────────────────────┴─────────────────────────────────────┤
│ > submit coder "write a fibonacci function in Go"        │
└──────────────────────────────────────────────────────────┘
```

- **Left pane**: Agent list (name, capability, status, load from heartbeats)
- **Right pane**: Task feed (submitted, in-progress, completed, failed)
- **Bottom**: Command input for ad-hoc task submission
- **Detail view**: Select a task with Enter to see inputs, outputs, duration, errors

### Keybindings

- `Tab` — switch panes
- `Enter` — expand task detail
- `r` — replay selected task
- `w` — open web replay for selected task
- `/` — filter tasks
- `q` — quit

## 10. Replay Views

### TUI Replay (`swarm replay <task_id>`)

Grouped timeline per agent, color-coded:

```
─── coder ───────────────────────
  03:10:02 → Task received
  03:10:03 → GOAL: code
  03:10:05   TOOL: write main.go
  03:10:07   TOOL: bash go test
  03:10:09 ✓ GOAL: code (6.2s)

─── tester ──────────────────────
  03:10:09 → Task received
  03:10:10 → GOAL: test
  03:10:12   TOOL: bash go test ./...
  03:10:14 ✓ GOAL: test (4.8s)
```

Collapsible groups per agent. Expand/collapse with Enter.

### Web Replay (`swarm replay <task_id> --web`)

Generates a self-contained HTML file (inline CSS/JS, no server) with:

- Swimlane visualization (agents as lanes, tasks/goals/tools as blocks on timeline)
- Click to expand tool call details
- Zoom/pan on timeline
- Color-coded by status (success/fail/running)

Inspired by Charmbracelet Crush v0.36.0 stats feature.

Detection logic:
- If `$BROWSER` or `$DISPLAY` is set → auto-open
- Otherwise → print file path, user opens manually

File location: `/tmp/swarm-replay-<task_id>.html`

## 11. Persistence

```
~/.local/share/swarm/
├── tasks/
│   └── <task_id>.json           # TaskMessage + TaskResult pairs
├── sessions/
│   └── <date>/
│       └── <task_id>.jsonl      # Session logs (fetched from agents if debug mode)
├── agents/
│   └── <agent_id>.json          # Latest heartbeat snapshot
└── swarm.db                     # SQLite index for fast querying
```

- **SQLite** for indexing: task status, capability, timestamps, tags, duration
- **Filesystem** for raw data: task payloads, session logs, agent state
- SQLite enables `swarm history --capability coder --status failed --since 2026-03-01` without scanning files

## 12. Configuration

```toml
# ~/.config/swarm/config.toml
[nats]
url = "nats://localhost:4222"

[defaults]
timeout = "30s"          # Default wait time for results
output = "text"          # text | json

[replay]
web_browser = ""         # Override $BROWSER
```

Minimal. NATS URL is the only required field.

## 13. Technology Stack

| Component | Technology |
|-----------|-----------|
| Language | Go |
| CLI framework | Kong (match headless-agent) |
| TUI | Bubbletea + Lipgloss + Bubbles |
| NATS client | nats.go |
| Web replay | Go html/template → self-contained HTML |
| Persistence | SQLite (modernc.org/sqlite, CGO-free) + filesystem |
| Task types | agentkit/tasks (TaskMessage, TaskResult) |
| Heartbeat | agentkit/heartbeat |

## 14. Implementation Phases

### Phase 1: Core CLI

- `swarm status`, `swarm agents`, `swarm capabilities`
- `swarm submit`, `swarm result`, `swarm history`
- NATS connection, heartbeat discovery
- SQLite persistence
- Config file loading

### Phase 2: Manifest & Lifecycle

- `swarm.yaml` parsing
- `swarm up`, `swarm down`, `swarm restart`
- NATS server detection and auto-start
- Process management for locally spawned agents

### Phase 3: TUI Dashboard

- `swarm ui` with bubbletea
- Agent pane, task pane, command input
- Real-time updates via NATS subscriptions
- Task detail view

### Phase 4: Replay Integration

- `swarm replay <task_id>` TUI view (grouped timeline)
- `swarm replay <task_id> --web` HTML generation
- Swimlane visualization in web view
- GUI detection and auto-open

### Phase 5: Task Chaining

- `swarm chain` for linear pipes
- Output → input mapping between stages
- Error handling / short-circuit on failure

## 15. Open Questions

1. **Session log access**: How does `swarm` fetch session logs from agents? Agents store logs locally. Options: (a) shared filesystem, (b) NATS request to agent, (c) agents publish logs to NATS subject
2. **Agent restart behavior**: When `swarm restart` is called, should the agent resume in-flight tasks or abandon them?
3. **Task TTL**: How long are completed task results retained in persistence? Configurable? Auto-cleanup?
4. **Manifest environment variables**: Support `${ENV_VAR}` expansion in swarm.yaml paths?
5. **Multiple swarm files**: Support `swarm up -f production.yaml`? Or always `swarm.yaml` in cwd?
