# Chapter 1: Architecture

## Overview

The headless agent is a CLI tool that executes workflows defined in Agentfiles using LLMs.

![Architecture](images/01-architecture.png)

## Components

| Component | Description |
|-----------|-------------|
| CLI | Commands: run, validate, inspect, pack, verify, install, setup |
| Agentfile Parser | Lexer and parser for workflow DSL |
| Executor | Runs workflows through goals and steps |
| Tool Registry | Built-in tools + MCP server tools |
| Session Store | Tracks execution state |
| LLM Client | Multi-provider LLM abstraction |

## Configuration Files

| File | Purpose | Permissions |
|------|---------|-------------|
| agent.toml | Agent settings, LLM config, profiles | 0644 |
| credentials.toml | API keys | 0400 (required) |
| policy.toml | Tool permissions, security policy | 0644 |

Location: Current directory, or `~/.config/grid/` for credentials.

## API Key Loading

Priority order (highest to lowest):

1. Environment variables
2. `.env` file in current directory
3. `~/.config/grid/credentials.toml`

For credentials.toml, provider-specific sections override `[llm]`:

```toml
[llm]
api_key = "default-key"

[anthropic]
api_key = "anthropic-specific-key"
```

## CLI Commands

| Command | Description |
|---------|-------------|
| run | Execute a workflow |
| validate | Check syntax without running |
| inspect | Show workflow/package structure |
| pack | Create a signed package |
| verify | Verify package signature |
| install | Install a package |
| keygen | Generate signing key pair |
| setup | Interactive setup wizard |

## Protected Files

The agent cannot modify its own configuration:
- agent.toml
- policy.toml
- credentials.toml

This prevents privilege escalation.

## Transport

The agent uses stdio with JSON-RPC for ACP integration. For standalone use, it's a standard CLI.

---

Next: [Agentfile DSL](02-agentfile.md)
