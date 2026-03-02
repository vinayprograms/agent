# Swarm Examples

Example swarm configurations demonstrating multi-agent workflows via NATS.

Each example contains:
- `swarm.yaml` — manifest defining agents and NATS config
- `agents/` — Agentfile(s) for each agent in the swarm
- `config/` — shared agent.toml and policy.toml
- `README.md` — description and usage

## Prerequisites

- NATS server running (`nats-server` or via `swarm up`)
- `agent` binary built and in PATH
- `swarm` binary built and in PATH

## Examples

| # | Name | Agents | Pattern | Key Feature |
|---|------|--------|---------|-------------|
| 01 | Hello Swarm | 1 | Single agent | Minimal swarm setup |
| 02 | Code & Test | 2 | Linear chain | Coder → Tester pipeline |
| 03 | Full Dev Team | 3 | Linear chain | Coder → Tester → Documenter |
| 04 | Parallel Research | 3 | Fan-out | Multiple researchers, same capability |
| 05 | Content Pipeline | 3 | Linear chain | Writer → Editor → Fact-checker |
| 06 | Security Scan | 2 | Fan-out/merge | SAST + dependency audit |
| 07 | Translation Bureau | 4 | Fan-out | Same task, multiple languages |
| 08 | Data Pipeline | 3 | Linear chain | Extract → Transform → Load |
| 09 | Peer Review | 2 | Bidirectional | Coder writes, reviewer critiques |
| 10 | Summarizer Fleet | 1 | Scaled single | Same agent ×3 for throughput |
| 11 | Recipe Kitchen | 3 | Specialization | Chef → Nutritionist → Plater |
| 12 | Incident Response | 3 | Priority routing | Triage → Investigate → Remediate |
| 13 | Learning Coach | 2 | Iterative | Teacher generates, Quizzer tests |
| 14 | API Design Workshop | 3 | Linear chain | Spec → Implement → Test |
| 15 | Debate Arena | 2 | Adversarial | Proposer vs Opposer |
