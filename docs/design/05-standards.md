# Chapter 5: Standards Support

## Overview

The agent implements open protocols for interoperability:

| Protocol | Role | Purpose |
|----------|------|---------|
| MCP | Client | Connect to external tool servers |
| ACP | Server | Expose agent to code editors/IDEs |
| A2A | — | Agent-to-agent communication (planned) |

## MCP (Model Context Protocol)

MCP standardizes how AI models access external tools and data sources.

**Role:** The agent acts as an MCP **client**, connecting to MCP servers that provide tools.

### How It Works

1. Agent starts MCP server as subprocess
2. Handshake via JSON-RPC 2.0 over stdio
3. Agent queries available tools
4. Tools registered in agent's tool registry
5. Agent calls tools as needed during execution
6. Server process lifecycle tied to agent

### Configuration

```toml
[[mcp.servers]]
name = "filesystem"
command = ["mcp-server-filesystem", "/workspace"]

[[mcp.servers]]
name = "database"
command = ["mcp-server-postgres"]
env = { DATABASE_URL = "postgres://..." }
allowed_tools = ["query", "list_tables"]
```

### Tool Allowlist

| Setting | Behavior |
|---------|----------|
| `allowed_tools` not set | All tools from server available (with warning) |
| `allowed_tools = []` | All tools denied |
| `allowed_tools = ["a", "b"]` | Only listed tools available |

### Security

MCP tools return untrusted content. All MCP results are marked `trust=untrusted, type=data` for security verification.

## ACP (Agent Client Protocol)

ACP standardizes communication between code editors and coding agents.

**Role:** The agent acts as an ACP **server**, accepting connections from editors/IDEs.

### How It Works

1. Editor connects to agent via stdio
2. Agent advertises capabilities (JSON-RPC 2.0)
3. Editor sends prompt requests
4. Agent executes and streams responses
5. Session state maintained across prompts

### Capabilities

| Capability | Description |
|------------|-------------|
| loadSession | Agent can load/resume sessions |
| image | Agent accepts image inputs |
| audio | Agent accepts audio inputs |
| embeddedContext | Agent accepts file context |

### Agent Info

```toml
[acp]
name = "headless-agent"
version = "0.4.0"
```

## A2A (Agent-to-Agent Protocol)

**Status:** Planned, not yet implemented.

A2A will enable direct communication between agents:
- Peer discovery
- Capability negotiation
- Task delegation
- Result collection

Current sub-agent spawning uses internal mechanisms. A2A will standardize this for cross-system agent collaboration.

## Protocol Comparison

| Aspect | MCP | ACP | A2A |
|--------|-----|-----|-----|
| Agent role | Client | Server | Peer |
| Connects to | Tool servers | Editors/IDEs | Other agents |
| Transport | stdio | stdio | TBD |
| Wire format | JSON-RPC 2.0 | JSON-RPC 2.0 | TBD |

## Relationship to Supervision

Protocol interactions feed into the security system:

| Source | Trust Level |
|--------|-------------|
| MCP tool results | untrusted |
| ACP prompt requests | Configurable (user_trust) |
| A2A messages | untrusted (planned) |

Security verification runs regardless of source. See [Security Documentation](../security/README.md).

---

Next: [Persistence](06-persistence.md)
