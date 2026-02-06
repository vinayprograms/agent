# Chapter 4: Tool System

## Overview

Tools give the agent capabilities beyond text generation. The tool registry manages both built-in and external tools.

## Built-in Tools

### File Operations

| Tool | Description |
|------|-------------|
| read | Read file contents |
| write | Write content to file |
| edit | Find and replace text in file |
| glob | List files matching pattern |
| grep | Search for pattern in files |
| ls | List directory contents |

### Execution

| Tool | Description |
|------|-------------|
| bash | Execute shell commands |

### Web

| Tool | Description |
|------|-------------|
| web_fetch | Fetch and extract content from URL |
| web_search | Search the web (requires API key) |

### Memory

| Tool | Description |
|------|-------------|
| memory_read | Read value by key |
| memory_write | Write value by key |
| memory_list | List keys (with optional prefix) |
| memory_search | Search values |

### Sub-Agents

| Tool | Description |
|------|-------------|
| spawn_agent | Spawn a single sub-agent |
| spawn_agents | Spawn multiple sub-agents in parallel |

## Tool Interface

Every tool implements:

| Method | Purpose |
|--------|---------|
| Name() | Tool identifier |
| Description() | Human-readable description |
| Parameters() | JSON Schema for arguments |
| Execute() | Run the tool |

## Policy Enforcement

Tools check permissions against policy.toml:

```toml
[tools]
# Allowed paths for file operations
allowed_paths = ["/workspace", "/tmp"]

# Denied paths (takes precedence)
denied_paths = ["/etc", "/root/.ssh"]

# Allowed tools (empty = all allowed)
allowed_tools = []

# Denied tools
denied_tools = ["bash"]  # Disable shell access
```

**Policy checks happen before execution.** A denied operation returns an error without side effects.

## MCP Tools

External tools via Model Context Protocol. See [Standards Support](05-standards.md).

```toml
[[mcp.servers]]
name = "database"
command = ["mcp-server-postgres"]
env = { DATABASE_URL = "..." }
allowed_tools = ["query", "list_tables"]  # Optional allowlist
```

MCP tools appear in the registry alongside built-in tools.

## Tool Execution Flow

1. Agent requests tool call
2. Registry looks up tool by name
3. Policy check (paths, permissions)
4. Security check (Tier 1 patterns)
5. If untrusted content in context → Tier 2/3 verification
6. Tool executes
7. Result returned (marked as untrusted)

## Adding Custom Tools

Tools can be added via:

1. **MCP servers** — External processes, any language
2. **Go plugins** — Native tools, compiled in
3. **ACP exposure** — Agent capabilities exposed to others

---

Next: [Standards Support](05-standards.md)
