# 21 — Collaborative Incident Response

Same agents as [12-incident-response](../12-incident-response/), but using `discuss` mode. Triage, investigator, and remediator self-organize.

**Compare with:** [12-incident-response](../12-incident-response/) (explicit chain)

## Agents

- **triage** — classifies severity, estimates blast radius
- **investigator** — performs root cause analysis
- **remediator** — creates fix plan with rollback

## Usage

```bash
swarm up
swarm submit --mode discuss "API latency spiked to 30s, 502 errors at 40%, started 10 min ago right after deploying v2.3.1 — need severity assessment, root cause analysis, and a remediation plan"
swarm history
swarm down
```

## How It Works

All three agents should engage — the task explicitly requests assessment, analysis, and remediation. In a real incident, all three activities often happen in parallel anyway (triage while investigating while preparing rollback scripts).

## Pattern: Parallel Incident Response

```
         ┌─ triage      [EXECUTE — severity assessment]
discuss ─┼─ investigator [EXECUTE — root cause analysis]
         └─ remediator  [EXECUTE — fix plan]
```

Interesting comparison: the chain version (12) forces sequential triage → investigate → remediate. Collaboration allows all three to work simultaneously from the raw incident report. The remediator might produce a more generic playbook (without RCA input), but the triage and investigation results should be similar.
