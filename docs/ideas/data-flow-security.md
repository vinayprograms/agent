# Data Flow Security

## Problem

Even if individual tools/MCPs are secure, the **workflow** can be insecure. Connecting a bank MCP to an email MCP creates a sensitive data leak path — regardless of whether each integration is individually trustworthy.

This was observed in Zapier/Gumloop-style workflow builders and applies directly to headless-agent.

## Current Gap

Today's security model asks **"where did this come from?"** (trusted/vetted/untrusted sources).

It does NOT ask **"where can this go?"**

Example:
- Bank MCP returns financial data → tagged as `untrusted` source
- Email MCP accepts outbound messages → also tagged as `untrusted` source
- Both get the same treatment. No rule prevents `bank data → email send`.

The taint tracking system traces untrusted *instructions* (injection defense), not sensitive *data* with flow restrictions.

## Two Risk Surfaces

1. **Cross-agent flow (hive)** — Agent A delegates to Agent B, data flows between them via task inputs/outputs
2. **Intra-agent flow (headless-agent)** — Single agent has access to both sensitive sources and high-exposure sinks

## What's Needed

### 1. Data Sensitivity Classification

MCP/tool responses tagged with sensitivity levels:
- `public` — safe to share anywhere
- `internal` — org-internal, not for external sharing
- `pii` — personally identifiable information
- `financial` — banking, payment data
- `credentials` — secrets, tokens, keys

### 2. Sink Exposure Classification

Tools/MCPs tagged with exposure levels:
- `local` — stays on disk, within workspace
- `internal` — internal systems (databases, internal APIs)
- `external-private` — external but restricted (email to known recipients)
- `external-public` — public internet (social media, public APIs)

### 3. Directional Flow Rules

Policy-defined rules preventing sensitive data from reaching high-exposure sinks:

```
financial → local: ALLOW
financial → external-public: DENY
pii → external-*: DENY unless encrypted
credentials → *: DENY (never forward)
```

## Open Questions

- **Who classifies data?** MCP metadata? Agent policy file? LLM inference? Combination?
- **How are flow rules defined?** Per-MCP config in policy.toml? Separate flow-policy file?
- **Granularity:** Per-field (bank account number) vs per-response (entire bank API response)?
- **Performance:** Cost of tracking data lineage through multi-step tool call chains?
- **Cross-agent:** How do sensitivity tags propagate through hive task inputs/outputs?
- **Override mechanism:** How does a human authorize a normally-blocked flow?

## Relationship to Existing Security

This extends the current taint tracking concept:
- Current: taint = "this content may contain injected instructions"
- Extended: taint = "this content has sensitivity level X and flow restriction Y"

Both use the same block-tracking infrastructure. Data flow rules add a second dimension to the existing `trust` attribute.

## References

- Zapier/Gumloop workflow security analysis
- Discussed in swarm-agents group, filed here for headless-agent scope
