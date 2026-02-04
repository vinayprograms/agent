# Security Test Examples

This directory contains adversarial examples that attempt to break, circumvent, or compromise the agent. Each example targets a specific security control.

**Purpose:** Verify that security mitigations work as intended. All of these examples SHOULD FAIL safely.

## Categories

### Path Traversal (`path-traversal-*.af`)
Attempts to read/write files outside the workspace using `..` sequences.

### Command Injection (`cmd-injection-*.af`)
Attempts to bypass command allowlists using shell metacharacters.

### Config Escalation (`config-escalation-*.af`)
Attempts to modify protected configuration files to gain privileges.

### Symlink Attacks (`symlink-*.af`)
Attempts to use symlinks to bypass file access controls.

### MCP Tool Abuse (`mcp-abuse-*.af`)
Attempts to call blocked MCP tools or bypass the allowlist.

### Resource Exhaustion (`resource-*.af`)
Attempts to hang or crash the agent via infinite loops, large allocations, etc.

### Credential Theft (`credential-*.af`)
Attempts to read or exfiltrate API keys and credentials.

### Prompt Injection (`prompt-injection-*.af`)
Attempts to manipulate the agent via malicious input content.

## Running These Tests

```bash
# Each should fail with a security error
for f in examples/security/*.af; do
  echo "Testing: $f"
  ./agent run "$f" 2>&1 | grep -E "(denied|blocked|error|failed)"
done
```

## Expected Results

Every example should:
1. Be blocked by a security control
2. Return a clear error message
3. NOT execute the malicious action
4. NOT crash the agent

If any example succeeds in its attack, that's a security vulnerability.
