# Security Test Examples

This directory contains security test workflows in two categories:

## Adversarial Tests (SHOULD FAIL)

These attempt to break, circumvent, or compromise the agent. All should be blocked by security controls.

| Category | Count | Files |
|----------|-------|-------|
| Path Traversal | 3 | `path-traversal-*.af` |
| Command Injection | 7 | `cmd-injection-*.af` |
| Config Escalation | 4 | `config-escalation-*.af` |
| Symlink Attacks | 4 | `symlink-*.af`, `race-*.af` |
| Resource Exhaustion | 4 | `resource-*.af` |
| Credential Theft | 5 | `credential-*.af` |
| Prompt Injection | 4 | `prompt-injection-*.af` |
| MCP/Web Abuse | 4 | `mcp-abuse-*.af`, `web-abuse-*.af` |
| Sub-Agent Abuse | 2 | `subagent-*.af` |
| Tool Abuse | 5 | `glob-*.af`, `grep-*.af`, `edit-*.af`, `write-*.af`, `encoded-*.af` |
| Combined | 1 | `combined-multi-step.af` |

## Legitimate Tests (SHOULD SUCCEED)

These verify that normal, allowed operations still work correctly.

| Category | Files |
|----------|-------|
| File Operations | `legitimate-read-workspace.af`, `legitimate-write-workspace.af`, `legitimate-edit-workspace.af`, `legitimate-nested-dirs.af` |
| Shell Commands | `legitimate-bash-ls.af`, `legitimate-bash-go-test.af` |
| Search/Discovery | `legitimate-glob-workspace.af`, `legitimate-grep-workspace.af` |
| Web Access | `legitimate-web-fetch.af`, `legitimate-web-search.af` |
| Sub-Agents | `legitimate-spawn-agent.af` |
| Memory | `legitimate-memory-ops.af` |
| Policy-Dependent | `legitimate-absolute-allowed.af`, `legitimate-mcp-allowed.af` |
| Multi-Step | `legitimate-multi-step.af` |

## Running Tests

```bash
# Run all adversarial tests (expect failures)
for f in examples/security/path-traversal-*.af \
         examples/security/cmd-injection-*.af \
         examples/security/config-escalation-*.af \
         examples/security/symlink-*.af \
         examples/security/resource-*.af \
         examples/security/credential-*.af \
         examples/security/prompt-injection-*.af \
         examples/security/mcp-abuse-*.af \
         examples/security/web-abuse-*.af \
         examples/security/subagent-*.af; do
  echo "Testing (expect FAIL): $f"
  ./agent run "$f" 2>&1 | grep -E "(denied|blocked|error|failed|timeout)" && echo "✓ Blocked" || echo "✗ NOT BLOCKED!"
done

# Run all legitimate tests (expect success)
for f in examples/security/legitimate-*.af; do
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
