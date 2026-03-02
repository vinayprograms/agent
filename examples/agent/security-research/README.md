# Security Research Examples

These examples demonstrate how to use `SECURITY research` mode for legitimate security research, penetration testing, and vulnerability analysis.

## Overview

When conducting security research, normal security guardrails can interfere with legitimate work. The `SECURITY research` mode provides:

1. **Scope-aware security supervision** - Permissive within declared scope, strict at boundaries
2. **Defensive framing** - System prompts indicate authorized research context
3. **Full audit trail** - All actions still logged for accountability

## Usage

```
SECURITY research "your authorized scope description"
```

The scope description is **required** and should clearly define:
- What systems/networks are authorized targets
- What type of research is being conducted
- Any relevant authorization context

## Examples

### Basic Vulnerability Assessment

```
SECURITY research "vulnerability assessment of internal web application at app.internal.corp"
```

### Network Penetration Test

```
SECURITY research "authorized pentest of lab network 192.168.100.0/24"
```

### Malware Analysis

```
SECURITY research "static and dynamic analysis of malware samples in isolated sandbox"
```

## Important Notes

1. **Scope boundaries are enforced** - Actions targeting systems outside your declared scope will still be blocked or flagged

2. **Audit trail is always active** - All security decisions are logged and signed

3. **This is not a bypass** - The supervisor is research-aware, not disabled. Egregious scope violations will still be caught.

4. **LLM refusals may still occur** - If the frontier LLM refuses security-related prompts, consider using a different model profile for security research workflows

## Files in This Directory

- `vuln-assessment.agent` - Web application vulnerability assessment
- `network-pentest.agent` - Network penetration testing workflow
- `malware-analysis.agent` - Malware analysis in sandbox
- `ctf-solver.agent` - Capture The Flag challenge solver
- `security-audit-research.agent` - Security audit with research mode
