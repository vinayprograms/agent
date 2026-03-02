# 12 — Incident Response

Three-stage incident response pipeline. Simulates an on-call workflow from alert to remediation.

## Agents

- **triage** — classifies severity, estimates blast radius, suggests immediate actions
- **investigator** — performs root cause analysis using 5 Whys
- **remediator** — creates multi-horizon fix plan with rollback

## Usage

```bash
swarm up
swarm chain triage "API latency spiked to 30s, 502 errors at 40%, started 10 min ago after deploy v2.3.1" -> investigate -> remediate
swarm down
```

## Pattern: Escalation Chain

```
triage → investigate → remediate
```

Unlike a generic linear chain, this models an escalation pattern where each stage builds on the previous assessment. The triage agent's severity classification determines urgency of downstream work.
