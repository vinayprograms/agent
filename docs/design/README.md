# Design Documentation

This documentation describes the architecture and design of the headless agent.

## Contents

| Chapter | Title | Description |
|---------|-------|-------------|
| 1 | [Architecture](01-architecture.md) | System components and data flow |
| 2 | [Agentfile DSL](02-agentfile.md) | Workflow definition language |
| 3 | [LLM Integration](03-llm.md) | Native SDKs, provider adapters |
| 4 | [Tool System](04-tools.md) | Built-in tools, MCP, policies |
| 5 | [Sub-Agents](05-subagents.md) | Static and dynamic spawning |
| 6 | [Packaging](06-packaging.md) | Signed packages, installation |
| 7 | [Efficiency](07-efficiency.md) | Resource efficiency, output terseness |

## Related Documentation

- [Execution Model](../execution/README.md) — Four-phase execution, supervision modes
- [Security](../security/README.md) — Threat model, trust boundaries, verification

## Design Principles

1. **Headless** — No UI, stdio transport, designed for automation
2. **Declarative** — Agentfile DSL, flat syntax, no nesting
3. **Provider agnostic** — Native SDKs for multi-provider LLM support
4. **Standards-based** — MCP for tools, ACP for editor integration, Agent Skills
5. **Defense in depth** — Security at multiple layers
