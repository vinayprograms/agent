# Security Documentation

This documentation describes the security architecture of the headless agent system.

## Contents

| Chapter | Title | Description |
|---------|-------|-------------|
| 1 | [The Threat Model](01-threat-model.md) | What we're defending against |
| 2 | [Trust Boundaries](02-trust-boundaries.md) | How we classify content |
| 3 | [The Block System](03-block-system.md) | Structural separation of instruction and data |
| 4 | [Encoded Content Detection](04-encoded-content.md) | Detecting obfuscated payloads |
| 5 | [Tiered Verification](05-tiered-verification.md) | Efficient security checks |
| 6 | [Cryptographic Audit Trail](06-audit-trail.md) | Non-repudiable supervision records |
| 7 | [Security Modes](07-security-modes.md) | Default vs Paranoid configuration |
| 8 | [Testing Your Model](08-model-testing.md) | Evaluating LLM security compliance |

## Core Principle

LLMs cannot reliably distinguish instructions from data. Everything is tokens.

Our approach: **Defense in depth**

```
┌─────────────────────────────────────────────────────────┐
│                    DEFENSE LAYERS                       │
├─────────────────────────────────────────────────────────┤
│  1. Structural Tagging    - Mark content with metadata  │
│  2. LLM Instruction       - Tell model to respect tags  │
│  3. Pattern Detection     - Catch encoded payloads      │
│  4. Tiered Verification   - Check suspicious actions    │
│  5. Tool Restrictions     - Limit blast radius          │
│  6. Supervision           - Detect drift from intent    │
│  7. Audit Trail           - Enable forensic analysis    │
└─────────────────────────────────────────────────────────┘
```

No single layer is sufficient. Together, they raise the bar significantly.

## Quick Start

For most deployments, the default security mode provides good protection:

```toml
# agent.toml
[security]
mode = "default"
user_trust = "trusted"  # or "untrusted" for public deployments
```

For high-security environments:

```toml
[security]
mode = "paranoid"
user_trust = "untrusted"
```

See [Security Modes](07-security-modes.md) for details.
