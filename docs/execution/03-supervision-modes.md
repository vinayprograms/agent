# Chapter 3: Supervision Modes

## Three Levels of Oversight

The supervision system supports three modes, from lightest to strictest:

![Supervision Modes](images/03-supervision-modes.png)

## Mode Comparison

| Mode | Phases | Supervisor | Human | Use Case |
|------|--------|------------|-------|----------|
| UNSUPERVISED | COMMIT + EXECUTE | Never | Never | Dev/test, trusted ops |
| SUPERVISED | All four | When flagged | Never | Production |
| SUPERVISED HUMAN | All four | Always | Required | Critical/compliance |

## UNSUPERVISED

The fastest execution path. Only COMMIT and EXECUTE phases run.

```
NAME quick-analysis
GOAL analyze "Analyze the log file and report errors" UNSUPERVISED
```

**Behavior:**
- Agent commits and executes
- No reconciliation checks
- No supervisor oversight
- Checkpoints still captured for audit

**When to use:**
- Development and testing
- Trusted internal tools
- Low-risk operations
- Maximum performance needed

## SUPERVISED (Auto)

The default for production. All four phases run, but supervisor only engages when reconcile flags issues.

```
NAME data-pipeline
GOAL process "Process customer data and generate report" SUPERVISED
```

**Behavior:**
- Agent commits and executes
- Reconcile checks for drift signals
- Supervisor called only if flags raised
- Most steps pass without supervisor overhead

**When to use:**
- Production workloads
- Trusted users, standard workflows
- Balance of safety and performance

## SUPERVISED HUMAN

Maximum oversight. Supervisor always runs, and human approval required.

```
NAME production-deploy
GOAL deploy "Deploy version 2.1 to production servers" SUPERVISED HUMAN
```

**Behavior:**
- Agent commits and executes
- Reconcile checks run
- Supervisor always evaluates (even if no flags)
- Human must approve before continuing
- Execution pauses waiting for human

**When to use:**
- Critical operations (deployments, deletions)
- Sensitive data processing
- Compliance requirements
- High-risk tool usage

## Pre-Flight Check

Before execution begins, the system validates supervision requirements.

If SUPERVISED HUMAN is required but no human connection exists → **fail immediately**.

Don't start a workflow that will inevitably stall waiting for a human who isn't there.

## Configuration

### Global Supervision (in Agentfile)

Place at top of file — applies to all goals that don't have explicit modifiers:

```
SUPERVISED
NAME my-workflow
GOAL step1 "First goal - inherits SUPERVISED"
GOAL step2 "Second goal - also inherits SUPERVISED"
```

### Per-Goal Supervision

Modifier at end of GOAL line:

```
NAME mixed-workflow
GOAL read-config "Read configuration file" UNSUPERVISED
GOAL modify-db "Modify database records" SUPERVISED
GOAL delete-users "Delete user accounts" SUPERVISED HUMAN
```

### Default Mode (in agent.toml)

When not specified in Agentfile:

```toml
[supervision]
default_mode = "supervised"  # unsupervised | supervised | supervised_human
model = "claude-sonnet"
human_timeout_seconds = 300
```

## Mode Inheritance

| Global Setting | Goal Modifier | Result |
|----------------|---------------|--------|
| (none) | (none) | Uses default_mode from config |
| UNSUPERVISED | (none) | UNSUPERVISED |
| UNSUPERVISED | SUPERVISED | SUPERVISED |
| SUPERVISED | (none) | SUPERVISED |
| SUPERVISED | UNSUPERVISED | UNSUPERVISED |
| SUPERVISED | SUPERVISED HUMAN | SUPERVISED HUMAN |
| SUPERVISED HUMAN | (none) | SUPERVISED HUMAN |
| SUPERVISED HUMAN | UNSUPERVISED | **Error** (cannot downgrade) |

**Rule:** Goals can escalate supervision but cannot reduce it below global level when global is SUPERVISED HUMAN.

---

Next: [Workflow Processing](04-workflow-processing.md)
