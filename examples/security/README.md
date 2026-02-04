# Security Test Examples

This directory contains security test workflows in two categories:

## Adversarial Tests (SHOULD FAIL)

These attempt to break, circumvent, or compromise the agent. All should be blocked by security controls.

| Category | Count | Files |
|----------|-------|-------|
| Path Traversal | 3 | `path-traversal-*.agent` |
| Command Injection | 7 | `cmd-injection-*.agent` |
| Config Escalation | 4 | `config-escalation-*.agent` |
| Symlink Attacks | 4 | `symlink-*.agent`, `race-*.agent` |
| Resource Exhaustion | 4 | `resource-*.agent` |
| Credential Theft | 5 | `credential-*.agent` |
| Prompt Injection | 4 | `prompt-injection-*.agent` |
| MCP/Web Abuse | 4 | `mcp-abuse-*.agent`, `web-abuse-*.agent` |
| Sub-Agent Abuse | 2 | `subagent-*.agent` |
| Tool Abuse | 5 | `glob-*.agent`, `grep-*.agent`, `edit-*.agent`, `write-*.agent`, `encoded-*.agent` |
| Combined | 1 | `combined-multi-step.agent` |

## Legitimate Tests (SHOULD SUCCEED)

These verify that normal, allowed operations still work correctly.

| Category | Files |
|----------|-------|
| File Operations | `legitimate-read-workspace.agent`, `legitimate-write-workspace.agent`, `legitimate-edit-workspace.agent`, `legitimate-nested-dirs.agent` |
| Shell Commands | `legitimate-bash-ls.agent`, `legitimate-bash-go-test.agent` |
| Search/Discovery | `legitimate-glob-workspace.agent`, `legitimate-grep-workspace.agent` |
| Web Access | `legitimate-web-fetch.agent`, `legitimate-web-search.agent` |
| Sub-Agents | `legitimate-spawn-agent.agent` |
| Memory | `legitimate-memory-ops.agent` |
| Policy-Dependent | `legitimate-absolute-allowed.agent`, `legitimate-mcp-allowed.agent` |
| Multi-Step | `legitimate-multi-step.agent` |

## Running Tests

```bash
# Run all adversarial tests (expect failures)
for f in examples/security/path-traversal-*.agent \
         examples/security/cmd-injection-*.agent \
         examples/security/config-escalation-*.agent \
         examples/security/symlink-*.agent \
         examples/security/resource-*.agent \
         examples/security/credential-*.agent \
         examples/security/prompt-injection-*.agent \
         examples/security/mcp-abuse-*.agent \
         examples/security/web-abuse-*.agent \
         examples/security/subagent-*.agent; do
  echo "Testing (expect FAIL): $f"
  ./agent run "$f" 2>&1 | grep -E "(denied|blocked|error|failed|timeout)" && echo "✓ Blocked" || echo "✗ NOT BLOCKED!"
done

# Run all legitimate tests (expect success)
for f in examples/security/legitimate-*.agent; do
  echo "Testing (expect SUCCESS): $f"
  ./agent run "$f" 2>&1 && echo "✓ Passed" || echo "✗ Failed"
done
```

## Expected Results

**Adversarial tests should:**
1. Be blocked by a security control
2. Return a clear error message
3. NOT execute the malicious action
4. NOT crash the agent

**Legitimate tests should:**
1. Complete successfully
2. Produce expected output
3. Demonstrate security doesn't break functionality

If any adversarial test succeeds → security vulnerability.
If any legitimate test fails → security too restrictive.
