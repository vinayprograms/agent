# 06 — Security Scan

Two complementary security agents running in parallel. Submit the same codebase to both — each scans from a different angle.

## Agents

- **sast-scanner** — static analysis for code vulnerabilities
- **dep-auditor** — dependency and supply chain audit

## Usage

```bash
swarm up
# Submit to both in parallel
swarm submit sast "$(cat main.go)"
swarm submit dep-audit "$(cat go.mod)"
swarm history
swarm down
```

## Pattern: Parallel Independent Scans

```
         ┌─ sast-scanner
submit ──┤                  (independent, no data dependency)
         └─ dep-auditor
```

Both run simultaneously on different aspects of the same project. Results are consumed independently.
