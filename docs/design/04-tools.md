# Chapter 4: Tool System

## Built-in Tools

### File Operations

| Tool | Description |
|------|-------------|
| read | Read file contents |
| write | Write/create files |
| edit | Edit files (find/replace) |
| glob | Find files by pattern |
| grep | Search file contents |
| ls | List directory contents |

### Shell

| Tool | Description |
|------|-------------|
| bash | Execute shell commands (requires a `[tools.bash]` table in policy, or `default_deny = false`) |

> **Note:** The bash tool is **controlled by policy**. The tool registry is built in `cmd/agent` from the loaded policy: a tool is registered only when the policy enables it (a `[tools.<name>]` table, or `default_deny = false`), and each registered tool carries its policy guard (path guards for file tools, a domain guard for `web_fetch`, a shellguard gate for `bash`). Policy is the single source of truth. When enabled, bash is protected by a two-step security model (see [Bash Security](../security/10-bash-security.md)).

### Web

| Tool | Description |
|------|-------------|
| web_search | Search the web |
| web_fetch | Fetch and summarize URL content |

### Memory

| Tool | Description |
|------|-------------|
| remember | Write findings/insights/lessons to agent memory |
| recall | Search agent memory |
| scratchpad_read / scratchpad_write / scratchpad_list / scratchpad_search | Per-run scratch notes |

### Dynamic Agents

| Tool | Description |
|------|-------------|
| spawn_agents | Spawn sub-agents at runtime |

## Web Search Providers

`web_search` supports multiple providers with automatic fallback:

| Priority | Provider | API Key | Notes |
|----------|----------|---------|-------|
| 1 | Brave Search | BRAVE_API_KEY | Best quality |
| 2 | Tavily | TAVILY_API_KEY | Good for research |
| 3 | DuckDuckGo | None | Zero-config fallback |

## MCP (Model Context Protocol)

Connect to external MCP tool servers:

```toml
# agent.toml

[mcp.servers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]

[mcp.servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_TOKEN = "${GITHUB_TOKEN}" }
```

Tools from MCP servers are automatically discovered and available to workflows.

## Agent Skills

Load skills from directories containing SKILL.md:

```toml
[skills]
paths = ["./skills", "~/.agent/skills"]
```

Skill structure:
```
my-skill/
├── SKILL.md           # Required: frontmatter + instructions
├── scripts/           # Optional: executable scripts
├── references/        # Optional: additional docs
└── assets/            # Optional: templates, data
```

## Security Policy

Restrict tool access with policy.toml:

```toml
# Note: workspace is set in agent.toml ([agent].workspace), not here.
# A tool is enabled by the presence of its [tools.<name>] table; there is no
# per-tool "enabled" key. With default_deny = true, unlisted tools are off.
default_deny = true

[tools.read]
allow = ["$WORKSPACE/**"]
deny = ["**/.env", "**/*.key"]

[tools.write]
allow = ["$WORKSPACE/**"]

# Bash is controlled by policy — the table enables it. `deny` lists bare
# command names added to shellguard's built-in banned list.
[tools.bash]
deny = ["rm", "sudo"]

# `allow` holds domain patterns for web tools.
[tools.web_fetch]
allow = ["api.github.com", "*.example.com"]
```

Legacy keys (`enabled`, `allowlist`, `denylist`, `allow_domains`, `rate_limit`)
are rejected at load time with a message naming the replacement.

## MCP Tool Security

```toml
[mcp]
enabled = true
allow = [
  "filesystem:read_file",
  "filesystem:list_directory",
  "memory:*",
]
```

`enabled = false` disables all MCP tools. If `[mcp]` is not configured, agent logs a warning and allows all MCP tools (development mode).

---

Next: [Sub-Agents](05-subagents.md)
