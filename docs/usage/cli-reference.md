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
| `agent setup` | Interactive setup wizard |
| `agent serve` | Run as A2A/ACP server |
| `agent help` | Show help |
| `agent version` | Show version |

## Flags

| Flag | Description |
|------|-------------|
| `--config <path>` | Config file (default: `agent.toml`) |
| `--input key=value` | Provide input (repeatable) |
| `-f <path>` | Specify Agentfile path |
| `--policy <path>` | Security policy file |
| `--workspace <path>` | Override workspace directory |

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
