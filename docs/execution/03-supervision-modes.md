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
# Agentfile
UNSUPERVISED

NAME quick-analysis
GOAL "Analyze the log file"
  read logs/app.log
  summarize findings
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
# Agentfile
SUPERVISED

NAME data-pipeline
GOAL "Process customer data"
  fetch from API
  transform records
  store results
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
# Agentfile
SUPERVISED HUMAN

NAME production-deploy
GOAL "Deploy to production"
  run tests
  build artifacts
  deploy to servers
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

Before execution begins, the system validates supervision requirements:

```go
func (e *Executor) PreFlight() error {
    for _, step := range e.workflow.Steps {
        if step.RequiresHuman() && !e.hasHumanConnection() {
            return fmt.Errorf(
                "step %q requires SUPERVISED HUMAN but no human connection available",
                step.ID,
            )
        }
    }
    return nil
}
```

**If SUPERVISED HUMAN is required but no human connection exists → fail immediately.**

Don't start a workflow that will inevitably stall waiting for a human who isn't there.

## Configuration

### Global (in Agentfile)

```
# At top of file - applies to all steps
SUPERVISED

NAME my-workflow
...
```

### Per-Step Override

```
NAME mixed-workflow

# Unsupervised step
GOAL "Read configuration"
  read config.yaml

# Supervised step (overrides global)
SUPERVISED
GOAL "Modify database"
  update records

# Human-required step
SUPERVISED HUMAN
GOAL "Delete user data"
  remove records
```

### In agent.toml

```toml
[supervision]
# Default mode when not specified in Agentfile
default_mode = "supervised"  # unsupervised | supervised | supervised_human

# Model for supervisor
model = "claude-sonnet"

# Timeout waiting for human approval
human_timeout_seconds = 300
```

## Mode Inheritance

| Global Setting | Step Setting | Result |
|----------------|--------------|--------|
| UNSUPERVISED | (none) | UNSUPERVISED |
| UNSUPERVISED | SUPERVISED | SUPERVISED |
| SUPERVISED | (none) | SUPERVISED |
| SUPERVISED | UNSUPERVISED | UNSUPERVISED |
| SUPERVISED | SUPERVISED HUMAN | SUPERVISED HUMAN |
| SUPERVISED HUMAN | (none) | SUPERVISED HUMAN |
| SUPERVISED HUMAN | UNSUPERVISED | **Error** (cannot downgrade) |

**Rule:** Steps can escalate supervision but not reduce it below global level when global is SUPERVISED HUMAN.

---

Next: [Workflow Processing](04-workflow-processing.md)
