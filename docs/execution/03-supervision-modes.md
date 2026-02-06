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
UNSUPERVISED
NAME quick-analysis
GOAL "Analyze the log file and report errors"
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
SUPERVISED
NAME data-pipeline
GOAL "Process customer data and generate report"
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
SUPERVISED HUMAN
NAME production-deploy
GOAL "Deploy version 2.1 to production servers"
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

Place at top of file — applies to all goals:

```
SUPERVISED
NAME my-workflow
GOAL "First goal - supervised"
GOAL "Second goal - also supervised"
```

### Per-Goal Supervision

Override for specific goals:

```
NAME mixed-workflow
GOAL "Read configuration file"
SUPERVISED GOAL "Modify database records"
SUPERVISED HUMAN GOAL "Delete user accounts"
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

| Global Setting | Goal Setting | Result |
|----------------|--------------|--------|
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
