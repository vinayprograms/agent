# Chapter 4: Workflow Processing

## Agentfile Structure

The Agentfile defines a workflow using a flat, declarative syntax:

```
NAME deploy-service
INPUT version, environment

SUPERVISED

GOAL "Build and test"
  run tests
  build docker image

SUPERVISED HUMAN
GOAL "Deploy to {environment}"
  push image to registry
  update kubernetes deployment
  verify health checks
```

## Execution Flow

![Agentfile Execution Flow](images/04-agentfile-flow.png)

## Parsing Phase

The lexer/parser converts the Agentfile into an AST:

```go
type Workflow struct {
    Name        string
    Inputs      []Input
    Supervised  bool      // Global supervision flag
    HumanOnly   bool      // Global human requirement
    Steps       []Step
}

type Step struct {
    Kind        StepKind  // GOAL, AGENT, RUN
    Content     string
    Supervised  bool      // Step-level override
    HumanOnly   bool
}
```

## Pre-Flight Validation

Before execution begins:

1. **Check supervision requirements**
   - If any step requires SUPERVISED HUMAN
   - And no human connection available
   - → Fail immediately

2. **Validate inputs**
   - All declared INPUTs must be provided
   - Type checking if specified

3. **Tool availability**
   - Referenced tools must be registered
   - Permissions checked against policy

```go
func (e *Executor) PreFlight() error {
    // Check human requirements
    for _, step := range e.workflow.Steps {
        if step.RequiresHuman() && !e.hasHumanConnection() {
            return ErrNoHumanAvailable
        }
    }
    
    // Validate inputs
    for _, input := range e.workflow.Inputs {
        if _, ok := e.providedInputs[input.Name]; !ok {
            return fmt.Errorf("missing required input: %s", input.Name)
        }
    }
    
    return nil
}
```

## Step Execution

Each step goes through the appropriate phases:

```go
func (e *Executor) executeStep(step Step) error {
    // Phase 1: COMMIT
    preCP, err := e.commitPhase(step)
    if err != nil {
        return err
    }
    e.checkpoints.SavePre(step.ID, preCP)
    
    // Phase 2: EXECUTE
    postCP, err := e.executePhase(step, preCP)
    if err != nil {
        return err
    }
    e.checkpoints.SavePost(step.ID, postCP)
    
    // Skip remaining phases if unsupervised
    if !e.isSupervised(step) {
        return nil
    }
    
    // Phase 3: RECONCILE
    flags := e.reconcilePhase(postCP)
    
    // Phase 4: SUPERVISE (if needed)
    if len(flags) > 0 || e.requiresHuman(step) {
        verdict, err := e.supervisePhase(step, preCP, postCP, flags)
        if err != nil {
            return err
        }
        
        switch verdict.Action {
        case CONTINUE:
            return nil
        case REORIENT:
            return e.reorientAndRetry(step, verdict.Correction)
        case PAUSE:
            return e.pauseForHuman(step, verdict)
        }
    }
    
    return nil
}
```

## Sub-Agent Spawning

The `spawn_agents` tool enables parallel sub-agent execution:

![Sub-Agent Model](images/04-sub-agent-model.png)

**Key constraints:**

| Property | Value |
|----------|-------|
| Maximum depth | 1 (no nested spawning) |
| State sharing | None (complete isolation) |
| Context | Separate per sub-agent |
| Permissions | Own policy, cannot escalate |
| Return | Result only, no side channels |

**Example:**

```
NAME orchestrator

GOAL "Analyze multiple repos"
  spawn_agents:
    - task: "Analyze repo A"
      agent: code-reviewer
    - task: "Analyze repo B"  
      agent: code-reviewer
    - task: "Analyze repo C"
      agent: code-reviewer
  
  collect results
  generate combined report
```

**Sub-agent execution:**
- All three run in parallel
- Each has isolated context
- Results collected by orchestrator
- Sub-agents cannot spawn further agents

## Error Handling

| Error Type | Behavior |
|------------|----------|
| Tool failure | Captured in post-checkpoint, may trigger reconcile |
| LLM error | Retry with backoff, fail after 5 attempts |
| Supervision timeout | PAUSE verdict, escalate to human if available |
| Human timeout | Configurable: fail or continue with warning |
| Pre-flight failure | Immediate abort before execution |

## Workflow Completion

A workflow completes when:
- All steps execute successfully, OR
- A step receives PAUSE verdict with no human resolution, OR
- An unrecoverable error occurs

**On completion:**
- Final checkpoint saved
- Session log finalized
- Results returned to caller

---

Next: [Supervisor Verdicts](05-supervisor-verdicts.md)
