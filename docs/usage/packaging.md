# Packaging

Create distributable, signed agent packages.

## Generate Signing Keys

```bash
./agent keygen -o my-key
# Creates: my-key.pem (private, keep secret!) and my-key.pub (share for verification)
```

## Create a Package

```bash
./agent pack ./my-agent \
  --sign my-key.pem \
  --author "Your Name" \
  --email "you@example.com" \
  --license MIT \
  -o my-agent-1.0.0.agent
```

## Package Structure

```
my-agent/
├── Agentfile           # Required: workflow definition
├── manifest.json       # Optional: additional metadata (outputs, deps)
├── agents/             # Agent persona files
├── goals/              # Goal prompt files
└── policy.toml         # Default security policy
```

## Manifest (Optional)

Create `manifest.json` for additional metadata not in Agentfile:

```json
{
  "name": "my-agent",
  "version": "1.0.0",
  "description": "What this agent does",
  "outputs": {
    "report": {"type": "string", "description": "Generated report"}
  },
  "dependencies": {
    "helper-agent": "^1.0.0"
  }
}
```

Note: `name`, `version`, `inputs`, and `requires` are auto-extracted from Agentfile.

## Verify a Package

```bash
./agent verify my-agent-1.0.0.agent --key author.pub
```

## Inspect a Package

```bash
./agent inspect my-agent-1.0.0.agent
```

## Install a Package

```bash
# Install with dependencies
./agent install my-agent-1.0.0.agent

# Skip dependencies (for A2A setups)
./agent install my-agent-1.0.0.agent --no-deps

# Preview what would be installed
./agent install my-agent-1.0.0.agent --dry-run
```

Packages install to `~/.agent/packages/<name>/<version>/`.

---

Back to [README](../../README.md) | See also: [CLI Reference](cli-reference.md)
