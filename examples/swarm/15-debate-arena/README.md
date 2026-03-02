# 15 — Debate Arena

Two agents with opposing mandates argue the same motion. Tests adversarial/complementary agent patterns.

## Agents

- **proposer** — argues FOR the motion
- **opposer** — argues AGAINST the motion

## Usage

```bash
swarm up
# Submit same motion to both
swarm submit propose "AI agents should have direct access to production databases"
swarm submit oppose "AI agents should have direct access to production databases"
swarm history
swarm down
```

## Pattern: Adversarial Parallel

```
         ┌─ proposer (FOR)
motion ──┤
         └─ opposer (AGAINST)
```

Both agents receive the same input but have opposing directives. Human reads both arguments to form a balanced view. Tests that agents with conflicting mandates produce genuinely different, high-quality output rather than hedging.
