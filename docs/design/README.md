# Design Documentation

This documentation describes the overall architecture and design of the headless agent system.

## Contents

| Chapter | Title | Description |
|---------|-------|-------------|
| 1 | [Architecture Overview](01-architecture.md) | System components and data flow |
| 2 | [Agentfile DSL](02-agentfile.md) | Workflow definition language |
| 3 | [LLM Integration](03-llm.md) | Fantasy framework, providers, credentials |
| 4 | [Tool System](04-tools.md) | Registry, built-in tools, policies |
| 5 | [Standards Support](05-standards.md) | MCP, ACP, and protocol integrations |
| 6 | [Persistence](06-persistence.md) | Sessions, memory, checkpoints |
| 7 | [Sub-Agents](07-subagents.md) | Spawning, isolation, orchestration |

## Related Documentation

- [Execution Model](../execution/README.md) — Four-phase execution, supervision modes, verdicts
- [Security](../security/README.md) — Threat model, trust boundaries, verification

## Design Principles

1. **Headless first** — No UI, stdio transport, designed for automation
2. **Declarative workflows** — Agentfile DSL, flat syntax, no nesting
3. **Defense in depth** — Multiple security layers, always-on verification
4. **Standards-based** — MCP for tools, ACP for exposing capabilities
5. **Provider agnostic** — Fantasy abstraction supports multiple LLM providers
