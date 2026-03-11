# 24 — Login Portal (Managed Swarm)

A managed swarm that builds a modern login portal with SSO/OAuth2 support.
Demonstrates the isolated execution model: one orchestrator decomposes
the project, three developer replicas each build a feature in isolation.

## The Problem

Build a modern-looking login portal with:
- Support for popular SSO and OAuth2 providers (Google, GitHub, Microsoft)
- A Go webserver that serves the portal pages
- A backend service handling all login operations
- Mocked SSO/OAuth2 integrations for local development testing

## Agents

- **orchestrator** (manager) — decomposes the project into 3 independent
  features, assigns them to workers, monitors progress via `discuss.*`,
  sends corrections via `work.<instance-id>.*` if workers drift.

- **developer** ×3 (worker replicas) — each builds one feature end-to-end
  (frontend + backend + tests) in complete isolation. No awareness of
  other workers' assignments.

## Usage

```bash
# Start NATS (if not running)
nats-server -js &

# Start the swarm
cd examples/swarm/24-login-portal
swarm up

# In another terminal: submit the project task
swarm submit develop "Write a modern looking login portal with support for popular SSO and OAuth2 providers (Google, GitHub, Microsoft). Write a webserver in Go that serves pages for this portal. Write a backend service in Go that handles all the login operations and serves the login portal. This backend service should mock the SSO and OAuth2 integrations so that the whole system can be tested on a local development environment. No npm or Node.js — frontend must use vanilla JS and CSS (CDN libraries are fine)."

# Monitor progress
swarm ui

# Stop
swarm down
```

## How It Works

1. `swarm up` starts the orchestrator + 3 developer replicas.
2. `swarm submit develop "..."` publishes the task to `work.develop.*`.
3. NATS queue group delivers the task to ONE developer (the others stay idle).
4. That developer builds its assigned feature, posting progress to `discuss.*`.
5. The orchestrator monitors `discuss.*` and can send corrections.

**Note:** In the current design, the orchestrator decomposes tasks and
assigns them. The initial `swarm submit` sends the raw task to one worker.
For full orchestrator-driven decomposition, submit to the orchestrator
directly: `swarm submit orchestrator "..."` (requires orchestrator to have
tools for publishing to `work.develop.*`).

## Pattern: Managed Parallel (Isolated Execution)

```
                      ┌─ developer-1 [feature: login UI + auth flow]
task → orchestrator ──┼─ developer-2 [feature: SSO/OAuth mock providers]
                      └─ developer-3 [feature: session management + webserver]
```

Workers never see each other's output. The orchestrator is the only entity
with full system awareness.
