# Hello World Agent

A minimal example agent demonstrating the packaging format.

## Usage

```bash
# Pack (unsigned)
agent pack ./hello-world -o hello-world.agent

# Pack (signed)
agent keygen -o my-key
agent pack ./hello-world --sign my-key.pem -o hello-world.agent

# Verify
agent verify hello-world.agent --key my-key.pub

# Inspect
agent inspect hello-world.agent

# Install
agent install hello-world.agent
```

## Inputs

- `topic` (optional): Topic for the greeting. Default: "programming"
