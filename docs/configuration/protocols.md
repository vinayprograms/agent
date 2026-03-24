# Protocol Support

The agent integrates with external tools and editors via MCP and ACP protocols.

## MCP (Model Context Protocol)

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

## Agent Skills (agentskills.io)

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

## ACP (Agent Client Protocol)

The agent can run as an ACP server for editor integration:

```bash
./agent acp --config agent.json
```

This enables communication with code editors (VS Code, JetBrains, Zed) that support ACP.

---

Back to [README](../../README.md) | See also: [Web Search](web-search.md)
