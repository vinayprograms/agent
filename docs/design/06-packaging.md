# Chapter 6: Packaging

## Overview

Create distributable, signed agent packages.

## Package Structure

```
my-agent/
├── Agentfile           # Required: workflow definition
├── manifest.json       # Optional: additional metadata
├── agents/             # Agent persona files
├── goals/              # Goal prompt files
└── policy.toml         # Default security policy
```

## Commands

| Command | Description |
|---------|-------------|
| keygen | Generate signing key pair |
| pack | Create a signed package |
| verify | Verify package signature |
| inspect | Show package structure |
| install | Install a package |

## Generate Signing Keys

```bash
./agent keygen -o my-key
# Creates: my-key.pem (private) and my-key.pub (public)
```

Keep the private key secret. Share the public key for verification.

## Create a Package

```bash
./agent pack ./my-agent \
  --sign my-key.pem \
  --author "Your Name" \
  --email "you@example.com" \
  --license MIT \
  -o my-agent-1.0.0.agent
```

## Manifest (Optional)

Create manifest.json for metadata not in Agentfile:

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

Note: name, version, inputs, and requires are auto-extracted from Agentfile.

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

**End of Design Documentation**

Return to [Overview](README.md) | See also [Execution Model](../execution/README.md) | [Security](../security/README.md)
