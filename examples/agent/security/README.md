# Security Examples

This directory contains example Agentfiles demonstrating security features.

## Examples

### basic-security.agent
Demonstrates a workflow with default security mode. The verifier performs tiered checks on tool calls when untrusted content is present.

### paranoid-security.agent
Demonstrates paranoid security mode where all tool calls with untrusted content go directly to Tier 3 (full supervisor) verification, skipping the cheaper Tier 2 triage.

### encoded-detection.agent
Shows how the security system detects and flags encoded content (Base64, hex, URL encoding) in untrusted inputs.

## Running Examples

```bash
cd examples/security
agent run basic-security.agent
```

## Security Modes

- **default**: Tiered verification (T1 → T2 → T3). Fast and efficient for most cases.
- **paranoid**: Skip Tier 2, go directly to full supervision for maximum security.

## Configuration

Security can be configured in `agent.toml`:

```toml
[security]
mode = "default"        # or "paranoid"
user_trust = "untrusted" # trust level for user messages
```
