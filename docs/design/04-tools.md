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
| bash | Execute shell commands (policy-controlled) |

### Web

| Tool | Description |
|------|-------------|
| web_search | Search the web |
| web_fetch | Fetch and summarize URL content |

### Memory

| Tool | Description |
|------|-------------|
| memory_read | Read from agent memory |
| memory_write | Write to agent memory |

### Dynamic Agents

| Tool | Description |
|------|-------------|
| spawn_agent | Spawn sub-agents at runtime |

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
```

## MCP Tool Security

```toml
[mcp]
default_deny = true
allowed_tools = [
  "filesystem:read_file",
  "filesystem:list_directory",
  "memory:*",
]
```

If [mcp] is not configured, agent logs a warning and allows all MCP tools (development mode).

---

Next: [Sub-Agents](05-subagents.md)
