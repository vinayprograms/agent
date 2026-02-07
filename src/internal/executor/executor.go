// Package executor provides workflow and goal execution.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/llm"
	"github.com/vinayprograms/agent/internal/logging"
	"github.com/vinayprograms/agent/internal/mcp"
	"github.com/vinayprograms/agent/internal/policy"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/skills"
	"github.com/vinayprograms/agent/internal/subagent"
	"github.com/vinayprograms/agent/internal/security"
	"github.com/vinayprograms/agent/internal/supervision"
	"github.com/vinayprograms/agent/internal/tools"
)

// Status represents the execution status.
type Status string

const (
	StatusRunning  Status = "running"
	StatusComplete Status = "complete"
	StatusFailed   Status = "failed"
)

// Result represents the execution result.
type Result struct {
	Status     Status
	Outputs    map[string]string
	Iterations map[string]int
	Error      string
}

// Executor executes workflows.
type Executor struct {
	workflow        *agentfile.Workflow
	provider        llm.Provider        // Default provider (backward compat)
	providerFactory llm.ProviderFactory // Profile-based providers
	registry        *tools.Registry
	policy          *policy.Policy
	logger          *logging.Logger

	// MCP support
	mcpManager *mcp.Manager

	// Skills support
	skillRefs    []skills.SkillRef
	loadedSkills map[string]*skills.Skill

	// Sub-agent support
	subAgentRunner *subagent.Runner
	packagePaths   []string

	// Session logging
	session        *session.Session
	sessionManager session.SessionManager
	currentGoal    string

	// State
	inputs  map[string]string
	outputs map[string]string

	// Supervision support
	checkpointStore *checkpoint.Store
	supervisor      *supervision.Supervisor
	humanAvailable  bool
	humanInputChan  chan string

	// Callbacks
	OnGoalStart        func(name string)
	OnGoalComplete     func(name string, output string)
	OnToolCall         func(name string, args map[string]interface{}, result interface{})
	OnToolError        func(name string, args map[string]interface{}, err error)
	OnLLMError         func(err error)
	OnSkillLoaded      func(name string)
	OnMCPToolCall      func(server, tool string, args map[string]interface{}, result interface{})
	OnSubAgentStart    func(name string, input map[string]string)
	OnSubAgentComplete func(name string, output string)
	OnSupervisionEvent func(stepID string, phase string, data interface{})

	// Security verifier
	securityVerifier *security.Verifier
}

// NewExecutor creates a new executor.
func NewExecutor(wf *agentfile.Workflow, provider llm.Provider, registry *tools.Registry, pol *policy.Policy) *Executor {
	e := &Executor{
		workflow:        wf,
		provider:        provider,
		providerFactory: llm.NewSingleProviderFactory(provider),
		registry:        registry,
		policy:          pol,
		logger:          logging.New().WithComponent("executor"),
		outputs:         make(map[string]string),
		loadedSkills:    make(map[string]*skills.Skill),
	}
	e.initSpawner()
	return e
}

// NewExecutorWithFactory creates an executor with a provider factory for profile support.
func NewExecutorWithFactory(wf *agentfile.Workflow, factory llm.ProviderFactory, registry *tools.Registry, pol *policy.Policy) *Executor {
	defaultProvider, _ := factory.GetProvider("")
	e := &Executor{
		workflow:        wf,
		provider:        defaultProvider,
		providerFactory: factory,
		registry:        registry,
		policy:          pol,
		logger:          logging.New().WithComponent("executor"),
		outputs:         make(map[string]string),
		loadedSkills:    make(map[string]*skills.Skill),
	}
	e.initSpawner()
	return e
}

// SetSecurityVerifier sets the security verifier for tool call verification.
func (e *Executor) SetSecurityVerifier(v *security.Verifier) {
	e.securityVerifier = v
	e.logger.Info("security verifier attached", nil)
}

// verifyToolCall checks a tool call against the security verifier if configured.
func (e *Executor) verifyToolCall(ctx context.Context, toolName string, args map[string]interface{}) error {
	if e.securityVerifier == nil {
		return nil // No security verifier configured
	}

	result, err := e.securityVerifier.VerifyToolCall(ctx, toolName, args, e.currentGoal)
	if err != nil {
		return fmt.Errorf("security verification error: %w", err)
	}

	// Log security decision to session
	if result.Tier1 != nil {
		blockID := ""
		if result.Tier1.Block != nil {
			blockID = result.Tier1.Block.ID
		}
		e.logSecurityStatic(toolName, blockID, result.Tier1.Pass, result.Tier1.Reasons)
	}

	if result.Tier2 != nil {
		blockID := ""
		if result.Tier1 != nil && result.Tier1.Block != nil {
			blockID = result.Tier1.Block.ID
		}
		e.logSecurityTriage(toolName, blockID, result.Tier2.Suspicious, "triage", 0)
	}

	if result.Tier3 != nil {
		blockID := ""
		if result.Tier1 != nil && result.Tier1.Block != nil {
			blockID = result.Tier1.Block.ID
		}
		e.logSecuritySupervisor(toolName, blockID, string(result.Tier3.Verdict), result.Tier3.Reason, "supervisor", 0)
	}

	// Determine check path
	checkPath := "static"
	if result.Tier2 != nil {
		checkPath = "static→triage"
	}
	if result.Tier3 != nil {
		checkPath = "static→triage→supervisor"
	}

	if !result.Allowed {
		e.logSecurityDecision(toolName, "deny", result.DenyReason, "", checkPath)
		return fmt.Errorf("security: %s", result.DenyReason)
	}

	e.logSecurityDecision(toolName, "allow", "verified", "", checkPath)
	return nil
}

// AddUntrustedContent registers untrusted content with the security verifier.
func (e *Executor) AddUntrustedContent(content, source string) {
	if e.securityVerifier == nil {
		return
	}
	block := e.securityVerifier.AddBlock(security.TrustUntrusted, security.TypeData, true, content, source)

	// Log to session with XML representation
	xmlBlock := fmt.Sprintf(`<block id="%s" trust="untrusted" type="data" source="%s" mutable="true">%s</block>`,
		block.ID, source, truncateForLog(content, 200))
	entropy := security.ShannonEntropy([]byte(content))
	e.logSecurityBlock(block.ID, "untrusted", "data", source, xmlBlock, entropy)
}

// truncateForLog truncates a string for logging purposes.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// initSpawner wires up the spawn_agent tool to this executor
func (e *Executor) initSpawner() {
	if e.registry == nil {
		return
	}
	e.registry.SetSpawner(func(ctx context.Context, role, task string, outputs []string) (string, error) {
		return e.spawnDynamicAgent(ctx, role, task, outputs)
	})
}

// SetMCPManager sets the MCP manager for external tool access.
func (e *Executor) SetMCPManager(m *mcp.Manager) {
	e.mcpManager = m
}

// SetSkills sets available skills for the executor.
func (e *Executor) SetSkills(refs []skills.SkillRef) {
	e.skillRefs = refs
}

// SetPackagePaths sets the paths to search for sub-agent packages.
func (e *Executor) SetPackagePaths(paths []string) {
	e.packagePaths = paths
	e.initSubAgentRunner()
}

// SetSession sets the session for logging events.
func (e *Executor) SetSession(sess *session.Session, mgr session.SessionManager) {
	e.session = sess
	e.sessionManager = mgr
}

// SetSupervision configures supervision for the executor.
func (e *Executor) SetSupervision(store *checkpoint.Store, supervisorProvider llm.Provider, humanAvailable bool, humanInputChan chan string) {
	e.checkpointStore = store
	e.humanAvailable = humanAvailable
	e.humanInputChan = humanInputChan

	if supervisorProvider != nil {
		e.supervisor = supervision.New(supervision.Config{
			Provider:       supervisorProvider,
			HumanAvailable: humanAvailable,
			HumanInputChan: humanInputChan,
		})
	}
}

// PreFlight checks if the workflow can execute successfully.
// Returns an error if SUPERVISED HUMAN steps exist but no human connection is available.
func (e *Executor) PreFlight() error {
	if !e.workflow.HasHumanRequiredSteps() {
		return nil
	}

	if e.humanAvailable {
		return nil
	}

	// Get names of steps requiring human supervision
	names := e.workflow.GetHumanRequiredStepNames()
	return fmt.Errorf("workflow requires human supervision for steps [%s] but no human connection is available", strings.Join(names, ", "))
}

// logEvent logs an event to the session's chronological event stream.
func (e *Executor) logEvent(eventType, content string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      eventType,
		Goal:      e.currentGoal,
		Content:   content,
		Timestamp: time.Now(),
	})
	e.sessionManager.Update(e.session)
}

// logToolCall logs a tool call event to the session.
func (e *Executor) logToolCall(name string, args map[string]interface{}) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventToolCall,
		Goal:      e.currentGoal,
		Tool:      name,
		Args:      args,
		Timestamp: time.Now(),
	})
	e.sessionManager.Update(e.session)
}

// logToolResult logs a tool result event to the session.
func (e *Executor) logToolResult(name string, result interface{}, err error, duration time.Duration) {
	// Structured logging to stdout
	e.logger.ToolResult(name, duration, err)

	if e.session == nil || e.sessionManager == nil {
		return
	}

	// Convert result to string for content
	var content string
	switch v := result.(type) {
	case string:
		content = v
	case []byte:
		content = string(v)
	default:
		if b, err := json.Marshal(result); err == nil {
			content = string(b)
		} else {
			content = fmt.Sprintf("%v", result)
		}
	}

	event := session.Event{
		Type:       session.EventToolResult,
		Goal:       e.currentGoal,
		Tool:       name,
		Content:    content,
		DurationMs: duration.Milliseconds(),
		Timestamp:  time.Now(),
	}
	if err != nil {
		event.Error = err.Error()
	}
	e.session.Events = append(e.session.Events, event)
	e.sessionManager.Update(e.session)
}

// logGoalStart logs the start of a goal.
func (e *Executor) logGoalStart(goalName string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventGoalStart,
		Goal:      goalName,
		Content:   fmt.Sprintf("Starting goal: %s", goalName),
		Timestamp: time.Now(),
	})
	e.sessionManager.Update(e.session)
}

// logGoalEnd logs the end of a goal.
func (e *Executor) logGoalEnd(goalName, output string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventGoalEnd,
		Goal:      goalName,
		Content:   output,
		Timestamp: time.Now(),
	})
	e.sessionManager.Update(e.session)
}

// --- Forensic Session Logging ---

// logPhaseCommit logs the COMMIT phase to session.
func (e *Executor) logPhaseCommit(goal, commitment, confidence string, durationMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventPhaseCommit,
		Goal:       goal,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:      "COMMIT",
			Commitment: commitment,
			Confidence: confidence,
			Result:     confidence,
		},
	})
	e.sessionManager.Update(e.session)
}

// logPhaseExecute logs the EXECUTE phase to session.
func (e *Executor) logPhaseExecute(goal, result string, durationMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventPhaseExecute,
		Goal:       goal,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:  "EXECUTE",
			Result: result,
		},
	})
	e.sessionManager.Update(e.session)
}

// logPhaseReconcile logs the RECONCILE phase to session.
func (e *Executor) logPhaseReconcile(goal, step string, triggers []string, escalate bool, durationMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventPhaseReconcile,
		Goal:       goal,
		Step:       step,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:    "RECONCILE",
			Triggers: triggers,
			Escalate: escalate,
			Result:   fmt.Sprintf("escalate=%v", escalate),
		},
	})
	e.sessionManager.Update(e.session)
}

// logPhaseSupervise logs the SUPERVISE phase to session.
func (e *Executor) logPhaseSupervise(goal, step, verdict, guidance string, humanRequired bool, durationMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventPhaseSupervise,
		Goal:       goal,
		Step:       step,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			Phase:         "SUPERVISE",
			Verdict:       verdict,
			Guidance:      guidance,
			HumanRequired: humanRequired,
			Result:        verdict,
		},
	})
	e.sessionManager.Update(e.session)
}

// logCheckpoint logs a checkpoint save to session.
func (e *Executor) logCheckpoint(checkpointType, goal, step, checkpointID string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventCheckpoint,
		Goal:      goal,
		Step:      step,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			CheckpointType: checkpointType,
			CheckpointID:   checkpointID,
		},
	})
	e.sessionManager.Update(e.session)
}

// logSecurityBlock logs untrusted content registration to session.
func (e *Executor) logSecurityBlock(blockID, trust, blockType, source, xmlBlock string, entropy float64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventSecurityBlock,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			BlockID:   blockID,
			Trust:     trust,
			BlockType: blockType,
			Source:    source,
			Entropy:   entropy,
			XMLBlock:  xmlBlock,
		},
	})
	e.sessionManager.Update(e.session)
}

// logSecurityStatic logs static/deterministic check to session.
func (e *Executor) logSecurityStatic(tool, blockID string, pass bool, flags []string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventSecurityStatic,
		Tool:      tool,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			CheckName: "static",
			BlockID:   blockID,
			Pass:      pass,
			Flags:     flags,
		},
	})
	e.sessionManager.Update(e.session)
}

// logSecurityTriage logs LLM triage check to session.
func (e *Executor) logSecurityTriage(tool, blockID string, suspicious bool, model string, latencyMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventSecurityTriage,
		Tool:       tool,
		DurationMs: latencyMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			CheckName:  "triage",
			BlockID:    blockID,
			Suspicious: suspicious,
			Model:      model,
			LatencyMs:  latencyMs,
		},
	})
	e.sessionManager.Update(e.session)
}

// logSecuritySupervisor logs supervisor review to session.
func (e *Executor) logSecuritySupervisor(tool, blockID, verdict, reason, model string, latencyMs int64) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventSecuritySupervisor,
		Tool:       tool,
		DurationMs: latencyMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			CheckName: "supervisor",
			BlockID:   blockID,
			Verdict:   verdict,
			Reason:    reason,
			Model:     model,
			LatencyMs: latencyMs,
		},
	})
	e.sessionManager.Update(e.session)
}

// logSecurityDecision logs final security decision to session.
func (e *Executor) logSecurityDecision(tool, action, reason, trust, checkPath string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventSecurityDecision,
		Tool:      tool,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			Action:    action,
			Reason:    reason,
			Trust:     trust,
			CheckPath: checkPath,
		},
	})
	e.sessionManager.Update(e.session)
}

// initSubAgentRunner initializes the sub-agent runner.
func (e *Executor) initSubAgentRunner() {
	if e.providerFactory == nil || len(e.packagePaths) == 0 {
		return
	}
	e.subAgentRunner = subagent.NewRunner(e.providerFactory, e.packagePaths)
	e.subAgentRunner.OnSubAgentStart = e.OnSubAgentStart
	e.subAgentRunner.OnSubAgentComplete = e.OnSubAgentComplete
}

// spawnDynamicAgent spawns a sub-agent with the given role and task.
func (e *Executor) spawnDynamicAgent(ctx context.Context, role, task string, outputs []string) (string, error) {
	// Build system prompt for the sub-agent
	systemPrompt := fmt.Sprintf("You are a %s. Complete the following task thoroughly and return your findings.\n\nTask: %s", role, task)

	// Add structured output instruction if outputs specified
	userPrompt := task
	if len(outputs) > 0 {
		userPrompt += "\n\n" + buildStructuredOutputInstruction(outputs)
	}

	// Log the spawn
	if e.OnSubAgentStart != nil {
		e.OnSubAgentStart(role, map[string]string{"task": task})
	}

	// Create messages for the sub-agent
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	// Build tool definitions (excluding spawn_agent to enforce depth=1)
	var toolDefs []llm.ToolDef
	for _, def := range e.registry.Definitions() {
		if def.Name != "spawn_agent" {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			})
		}
	}

	// Execute sub-agent loop
	for {
		resp, err := e.provider.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			return "", fmt.Errorf("sub-agent LLM error: %w", err)
		}

		// If no tool calls, we're done
		if len(resp.ToolCalls) == 0 {
			if e.OnSubAgentComplete != nil {
				e.OnSubAgentComplete(role, resp.Content)
			}
			return resp.Content, nil
		}

		// Add assistant message with tool calls
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute tool calls in parallel
		toolMessages := e.executeToolsParallel(ctx, resp.ToolCalls)
		messages = append(messages, toolMessages...)
	}
}

// OrchestratorSystemPromptPrefix returns the prefix to inject when spawn_agent is available.
const OrchestratorSystemPromptPrefix = `You are an orchestrator. You can spawn sub-agents to handle specific tasks.

Consider delegating when:
- The task has distinct parts that can be handled independently
- Specialized expertise would help (research, analysis, critique, writing, etc.)
- Work can be parallelized for efficiency

Use spawn_agent(role, task) to delegate work. You coordinate the overall effort and synthesize results.

`

// Run executes the workflow.
func (e *Executor) Run(ctx context.Context, inputs map[string]string) (*Result, error) {
	startTime := time.Now()
	workflowName := e.workflow.Name
	if workflowName == "" {
		workflowName = "unnamed"
	}
	e.logger.ExecutionStart(workflowName)

	// Pre-flight check for SUPERVISED HUMAN requirements
	if err := e.PreFlight(); err != nil {
		e.logger.ExecutionComplete(workflowName, time.Since(startTime), string(StatusFailed))
		return &Result{Status: StatusFailed, Error: err.Error()}, err
	}

	// Set original goal for supervisor if supervision is enabled
	if e.supervisor != nil {
		// Build original goal from workflow description
		var goalDescriptions []string
		for _, goal := range e.workflow.Goals {
			goalDescriptions = append(goalDescriptions, goal.Outcome)
		}
		e.supervisor.SetOriginalGoal(strings.Join(goalDescriptions, "; "))
	}

	// Bind inputs
	if err := e.bindInputs(inputs); err != nil {
		e.logger.ExecutionComplete(workflowName, time.Since(startTime), string(StatusFailed))
		return &Result{Status: StatusFailed, Error: err.Error()}, err
	}

	result := &Result{
		Status:     StatusRunning,
		Outputs:    make(map[string]string),
		Iterations: make(map[string]int),
	}

	// Execute steps in order
	for _, step := range e.workflow.Steps {
		if step.Type == agentfile.StepRUN {
			if err := e.executeRunStep(ctx, step, result); err != nil {
				result.Status = StatusFailed
				result.Error = err.Error()
				e.logger.ExecutionComplete(workflowName, time.Since(startTime), string(StatusFailed))
				return result, err
			}
		} else if step.Type == agentfile.StepLOOP {
			if err := e.executeLoopStep(ctx, step, result); err != nil {
				result.Status = StatusFailed
				result.Error = err.Error()
				e.logger.ExecutionComplete(workflowName, time.Since(startTime), string(StatusFailed))
				return result, err
			}
		}
	}

	result.Status = StatusComplete
	result.Outputs = e.outputs
	e.logger.ExecutionComplete(workflowName, time.Since(startTime), string(StatusComplete))
	return result, nil
}

// bindInputs binds input values with defaults.
func (e *Executor) bindInputs(inputs map[string]string) error {
	e.inputs = make(map[string]string)

	for _, input := range e.workflow.Inputs {
		if val, ok := inputs[input.Name]; ok {
			e.inputs[input.Name] = val
		} else if input.Default != nil {
			e.inputs[input.Name] = *input.Default
		} else {
			return fmt.Errorf("required input missing: %s", input.Name)
		}
	}

	return nil
}

// executeRunStep executes a RUN step.
func (e *Executor) executeRunStep(ctx context.Context, step agentfile.Step, result *Result) error {
	for _, goalName := range step.UsingGoals {
		goal := e.findGoal(goalName)
		if goal == nil {
			return fmt.Errorf("goal not found: %s", goalName)
		}

		output, err := e.executeGoal(ctx, goal)
		if err != nil {
			return err
		}

		e.outputs[goalName] = output
		result.Iterations[goalName] = 1
	}
	return nil
}

// GoalResult contains the result of executing a goal.
type GoalResult struct {
	Output        string
	ToolCallsMade bool
}

// executeLoopStep executes a LOOP step.
func (e *Executor) executeLoopStep(ctx context.Context, step agentfile.Step, result *Result) error {
	maxIterations := 10 // default
	if step.WithinLimit != nil {
		maxIterations = *step.WithinLimit
	}

	for _, goalName := range step.UsingGoals {
		goal := e.findGoal(goalName)
		if goal == nil {
			return fmt.Errorf("goal not found: %s", goalName)
		}

		iterations := 0
		var lastOutput string
		for i := 0; i < maxIterations; i++ {
			iterations++

			gr, err := e.executeGoalWithTracking(ctx, goal)
			if err != nil {
				return err
			}

			e.outputs[goalName] = gr.Output

			// Convergence: same output as last iteration
			if gr.Output == lastOutput {
				break
			}
			lastOutput = gr.Output

			// Convergence: no tool calls made = nothing more to do
			if !gr.ToolCallsMade {
				break
			}
		}
		result.Iterations[goalName] = iterations
	}
	return nil
}

// executeGoal executes a single goal (wrapper for backwards compatibility).
func (e *Executor) executeGoal(ctx context.Context, goal *agentfile.Goal) (string, error) {
	gr, err := e.executeGoalWithTracking(ctx, goal)
	if err != nil {
		return "", err
	}
	return gr.Output, nil
}

// isSupervised determines if a goal should be supervised based on goal settings and workflow defaults.
func (e *Executor) isSupervised(goal *agentfile.Goal) bool {
	// Goal-level override takes precedence
	if goal.Supervised != nil {
		return *goal.Supervised
	}
	// Fall back to workflow-level default
	return e.workflow.Supervised
}

// requiresHuman determines if a goal requires human approval.
func (e *Executor) requiresHuman(goal *agentfile.Goal) bool {
	// Goal-level override takes precedence
	if goal.Supervised != nil && *goal.Supervised {
		return goal.HumanOnly
	}
	// Fall back to workflow-level default
	if e.workflow.Supervised {
		return e.workflow.HumanOnly
	}
	return false
}

// executeGoalWithTracking executes a single goal with four-phase execution when supervision is enabled.
// Phases: COMMIT -> EXECUTE -> RECONCILE -> SUPERVISE
// All steps capture checkpoints; only supervised steps run RECONCILE/SUPERVISE.
func (e *Executor) executeGoalWithTracking(ctx context.Context, goal *agentfile.Goal) (*GoalResult, error) {
	// Log goal start
	e.logGoalStart(goal.Name)

	if e.OnGoalStart != nil {
		e.OnGoalStart(goal.Name)
	}

	// Check for multi-agent execution
	if len(goal.UsingAgent) > 0 {
		output, err := e.executeMultiAgentGoal(ctx, goal)
		e.logGoalEnd(goal.Name, output)
		return &GoalResult{Output: output, ToolCallsMade: false}, err
	}

	// Build prompt with context from previous goals
	prompt := e.interpolate(goal.Outcome)

	// Add context from prior goal outputs
	if len(e.outputs) > 0 {
		var priorContext strings.Builder
		priorContext.WriteString("## Context from Previous Goals\n\n")
		for goalName, output := range e.outputs {
			priorContext.WriteString(fmt.Sprintf("### %s\n%s\n\n", goalName, output))
		}
		prompt = priorContext.String() + "## Current Goal\n" + prompt
	}

	// Add structured output instruction if outputs are declared
	if len(goal.Outputs) > 0 {
		prompt += "\n\n" + buildStructuredOutputInstruction(goal.Outputs)
	}

	// Set current goal for logging
	e.currentGoal = goal.Name

	// Determine supervision status
	supervised := e.isSupervised(goal)
	humanRequired := e.requiresHuman(goal)

	// ============================================
	// PHASE 1: COMMIT - Agent declares intent
	// ============================================
	var preCheckpoint *checkpoint.PreCheckpoint
	if e.checkpointStore != nil {
		preCheckpoint = e.commitPhase(ctx, goal, prompt)
		if err := e.checkpointStore.SavePre(preCheckpoint); err != nil {
			e.logger.Warn("failed to save pre-checkpoint", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			e.logCheckpoint("pre", goal.Name, "", preCheckpoint.StepID)
		}
		if e.OnSupervisionEvent != nil {
			e.OnSupervisionEvent(goal.Name, "commit", preCheckpoint)
		}
	}

	// ============================================
	// PHASE 2: EXECUTE - Do the work
	// ============================================
	output, toolsUsed, toolCallsMade, err := e.executePhase(ctx, goal, prompt)
	if err != nil {
		return nil, err
	}

	// Create post-checkpoint with self-assessment
	var postCheckpoint *checkpoint.PostCheckpoint
	if e.checkpointStore != nil && preCheckpoint != nil {
		postCheckpoint = e.createPostCheckpoint(ctx, goal, preCheckpoint, output, toolsUsed)
		if err := e.checkpointStore.SavePost(postCheckpoint); err != nil {
			e.logger.Warn("failed to save post-checkpoint", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			e.logCheckpoint("post", goal.Name, "", postCheckpoint.StepID)
		}
		if e.OnSupervisionEvent != nil {
			e.OnSupervisionEvent(goal.Name, "execute", postCheckpoint)
		}
	}

	// ============================================
	// PHASE 3 & 4: RECONCILE & SUPERVISE (only for supervised steps)
	// ============================================
	if supervised && e.supervisor != nil && preCheckpoint != nil && postCheckpoint != nil {
		// RECONCILE: Static pattern checks
		reconcileStart := time.Now()
		reconcileResult := e.supervisor.Reconcile(preCheckpoint, postCheckpoint)
		reconcileDuration := time.Since(reconcileStart).Milliseconds()

		if e.checkpointStore != nil {
			if err := e.checkpointStore.SaveReconcile(reconcileResult); err != nil {
				e.logger.Warn("failed to save reconcile result", map[string]interface{}{
					"error": err.Error(),
				})
			} else {
				e.logCheckpoint("reconcile", goal.Name, reconcileResult.StepID, reconcileResult.StepID)
			}
		}
		// Log reconcile phase to session
		e.logPhaseReconcile(goal.Name, reconcileResult.StepID, reconcileResult.Triggers, reconcileResult.Supervise, reconcileDuration)

		if e.OnSupervisionEvent != nil {
			e.OnSupervisionEvent(goal.Name, "reconcile", reconcileResult)
		}

		// SUPERVISE: LLM evaluation (only if reconcile triggered)
		if reconcileResult.Supervise {
			superviseStart := time.Now()
			decisionTrail := e.checkpointStore.GetDecisionTrail()
			superviseResult, err := e.supervisor.Supervise(
				ctx,
				preCheckpoint,
				postCheckpoint,
				reconcileResult.Triggers,
				decisionTrail,
				humanRequired,
			)
			superviseDuration := time.Since(superviseStart).Milliseconds()

			if err != nil {
				return nil, fmt.Errorf("supervision failed: %w", err)
			}

			if e.checkpointStore != nil {
				if err := e.checkpointStore.SaveSupervise(superviseResult); err != nil {
					e.logger.Warn("failed to save supervise result", map[string]interface{}{
						"error": err.Error(),
					})
				} else {
					e.logCheckpoint("supervise", goal.Name, superviseResult.StepID, superviseResult.StepID)
				}
			}
			// Log supervise phase to session
			e.logPhaseSupervise(goal.Name, superviseResult.StepID, superviseResult.Verdict, superviseResult.Correction, humanRequired, superviseDuration)

			if e.OnSupervisionEvent != nil {
				e.OnSupervisionEvent(goal.Name, "supervise", superviseResult)
			}

			// Handle verdict
			switch supervision.Verdict(superviseResult.Verdict) {
			case supervision.VerdictReorient:
				// Apply correction and re-execute
				e.logger.Info("reorienting execution", map[string]interface{}{
					"goal":       goal.Name,
					"correction": superviseResult.Correction,
				})
				// Append correction to prompt and re-execute
				correctedPrompt := prompt + "\n\n## Supervisor Correction\n" + superviseResult.Correction
				output, _, toolCallsMade, err = e.executePhase(ctx, goal, correctedPrompt)
				if err != nil {
					return nil, err
				}

			case supervision.VerdictPause:
				// This should have been handled in Supervise() - if we get here, something is wrong
				return nil, fmt.Errorf("supervision paused but no resolution provided")
			}
		}
	}

	// Parse structured output if declared
	if len(goal.Outputs) > 0 {
		parsedOutputs, err := parseStructuredOutput(output, goal.Outputs)
		if err != nil {
			e.logEvent(session.EventSystem, fmt.Sprintf("Warning: failed to parse structured output: %v", err))
		} else {
			for field, value := range parsedOutputs {
				e.outputs[field] = value
			}
		}
	}

	if e.OnGoalComplete != nil {
		e.OnGoalComplete(goal.Name, output)
	}
	e.logGoalEnd(goal.Name, output)
	return &GoalResult{Output: output, ToolCallsMade: toolCallsMade}, nil
}

// commitPhase asks the agent to declare its intent before execution.
func (e *Executor) commitPhase(ctx context.Context, goal *agentfile.Goal, prompt string) *checkpoint.PreCheckpoint {
	start := time.Now()
	e.logger.PhaseStart("COMMIT", goal.Name, "")

	commitPrompt := fmt.Sprintf(`Before executing this goal, declare your intent:

GOAL: %s

Respond with a JSON object:
{
  "interpretation": "How you understand this goal",
  "scope_in": ["What you will do"],
  "scope_out": ["What you will NOT do"],
  "approach": "Your planned approach",
  "tools_planned": ["tools you expect to use"],
  "predicted_output": "What you expect to produce",
  "confidence": "high|medium|low",
  "assumptions": ["Assumptions you are making"]
}`, prompt)

	messages := []llm.Message{
		{Role: "system", Content: "You are declaring your intent before executing a goal. Be specific and honest about your plans."},
		{Role: "user", Content: commitPrompt},
	}

	resp, err := e.provider.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})

	pre := &checkpoint.PreCheckpoint{
		StepID:      goal.Name,
		StepType:    "GOAL",
		Instruction: prompt,
		Timestamp:   time.Now(),
	}

	if err != nil {
		e.logger.Warn("commit phase LLM error", map[string]interface{}{"error": err.Error()})
		pre.Confidence = "low"
		pre.Assumptions = []string{"Failed to get commitment from agent"}
		e.logger.PhaseComplete("COMMIT", goal.Name, "", time.Since(start), "error")
		return pre
	}

	// Parse the JSON response
	jsonStr := extractJSON(resp.Content)
	if jsonStr != "" {
		var commitData struct {
			Interpretation  string   `json:"interpretation"`
			ScopeIn         []string `json:"scope_in"`
			ScopeOut        []string `json:"scope_out"`
			Approach        string   `json:"approach"`
			ToolsPlanned    []string `json:"tools_planned"`
			PredictedOutput string   `json:"predicted_output"`
			Confidence      string   `json:"confidence"`
			Assumptions     []string `json:"assumptions"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &commitData); err == nil {
			pre.Interpretation = commitData.Interpretation
			pre.ScopeIn = commitData.ScopeIn
			pre.ScopeOut = commitData.ScopeOut
			pre.Approach = commitData.Approach
			pre.ToolsPlanned = commitData.ToolsPlanned
			pre.PredictedOutput = commitData.PredictedOutput
			pre.Confidence = commitData.Confidence
			pre.Assumptions = commitData.Assumptions
		}
	}

	// Default confidence if not parsed
	if pre.Confidence == "" {
		pre.Confidence = "medium"
	}

	// Log commitment to session for forensics
	durationMs := time.Since(start).Milliseconds()
	e.logPhaseCommit(goal.Name, pre.Interpretation, pre.Confidence, durationMs)

	return pre
}

// executePhase runs the actual goal execution loop.
func (e *Executor) executePhase(ctx context.Context, goal *agentfile.Goal, prompt string) (output string, toolsUsed []string, toolCallsMade bool, err error) {
	start := time.Now()

	// Build system message with skills context
	systemMsg := "You are a helpful assistant executing a workflow goal."

	// If spawn_agent tool is available, inject orchestrator guidance
	if e.registry != nil && e.registry.Has("spawn_agent") {
		systemMsg = OrchestratorSystemPromptPrefix + systemMsg
	}

	if len(e.skillRefs) > 0 {
		systemMsg += "\n\nAvailable skills:\n"
		for _, ref := range e.skillRefs {
			systemMsg += fmt.Sprintf("- %s: %s\n", ref.Name, ref.Description)
		}
		systemMsg += "\nTo use a skill, include [use-skill:skill-name] in your response."
	}

	// Build messages
	messages := []llm.Message{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: prompt},
	}

	// Log initial messages
	e.logEvent(session.EventSystem, systemMsg)
	e.logEvent(session.EventUser, prompt)

	// Get tool definitions (built-in + MCP)
	toolDefs := e.getAllToolDefinitions()
	e.logger.Debug("tools available", map[string]interface{}{
		"count": len(toolDefs),
	})

	// Track tools used
	toolsUsedMap := make(map[string]bool)

	// Execute goal loop
	for {
		resp, err := e.provider.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			if e.OnLLMError != nil {
				e.OnLLMError(err)
			}
			e.logPhaseExecute(goal.Name, "error", time.Since(start).Milliseconds())
			return "", nil, toolCallsMade, fmt.Errorf("LLM error: %w", err)
		}

		// Log assistant response
		e.logEvent(session.EventAssistant, resp.Content)

		// Check for skill activation in response
		if skill := e.checkSkillActivation(resp.Content); skill != nil {
			skillContext := e.getSkillContext(skill)
			messages = append(messages, llm.Message{
				Role:    "assistant",
				Content: resp.Content,
			})
			skillMsg := fmt.Sprintf("[Skill loaded: %s]\n\n%s", skill.Name, skillContext)
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: skillMsg,
			})
			e.logEvent(session.EventUser, skillMsg)
			continue
		}

		// No tool calls = goal complete
		if len(resp.ToolCalls) == 0 {
			// Convert tools used map to slice
			for tool := range toolsUsedMap {
				toolsUsed = append(toolsUsed, tool)
			}
			e.logPhaseExecute(goal.Name, "complete", time.Since(start).Milliseconds())
			return resp.Content, toolsUsed, toolCallsMade, nil
		}

		toolCallsMade = true

		// Track tools used
		for _, tc := range resp.ToolCalls {
			toolsUsedMap[tc.Name] = true
		}

		// Add assistant message with tool calls
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute tool calls in parallel
		toolMessages := e.executeToolsParallel(ctx, resp.ToolCalls)
		messages = append(messages, toolMessages...)
	}
}

// createPostCheckpoint creates a post-checkpoint with self-assessment.
func (e *Executor) createPostCheckpoint(ctx context.Context, goal *agentfile.Goal, pre *checkpoint.PreCheckpoint, output string, toolsUsed []string) *checkpoint.PostCheckpoint {
	// Ask agent to self-assess
	assessPrompt := fmt.Sprintf(`You just completed a goal. Assess your work:

ORIGINAL GOAL: %s

YOUR COMMITMENT:
- Interpretation: %s
- Approach: %s
- Predicted output: %s

ACTUAL OUTPUT:
%s

TOOLS USED: %s

Respond with a JSON object:
{
  "met_commitment": true/false,
  "deviations": ["Any deviations from your plan"],
  "concerns": ["Any concerns about your output"],
  "unexpected": ["Anything unexpected that happened"]
}`, pre.Instruction, pre.Interpretation, pre.Approach, pre.PredictedOutput, output, strings.Join(toolsUsed, ", "))

	messages := []llm.Message{
		{Role: "system", Content: "You are honestly assessing whether your work met your commitment."},
		{Role: "user", Content: assessPrompt},
	}

	resp, err := e.provider.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})

	post := &checkpoint.PostCheckpoint{
		StepID:       goal.Name,
		ActualOutput: output,
		ToolsUsed:    toolsUsed,
		Timestamp:    time.Now(),
	}

	if err != nil {
		e.logger.Warn("post-checkpoint LLM error", map[string]interface{}{"error": err.Error()})
		post.MetCommitment = false
		post.Concerns = []string{"Failed to get self-assessment from agent"}
		return post
	}

	// Parse the JSON response
	jsonStr := extractJSON(resp.Content)
	if jsonStr != "" {
		var assessData struct {
			MetCommitment bool     `json:"met_commitment"`
			Deviations    []string `json:"deviations"`
			Concerns      []string `json:"concerns"`
			Unexpected    []string `json:"unexpected"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &assessData); err == nil {
			post.MetCommitment = assessData.MetCommitment
			post.Deviations = assessData.Deviations
			post.Concerns = assessData.Concerns
			post.Unexpected = assessData.Unexpected
		} else {
			// If parsing fails, assume commitment was met
			post.MetCommitment = true
		}
	} else {
		// If no JSON found, assume commitment was met
		post.MetCommitment = true
	}

	return post
}

// getAllToolDefinitions returns tool definitions from registry and MCP servers.
func (e *Executor) getAllToolDefinitions() []llm.ToolDef {
	var toolDefs []llm.ToolDef

	// Built-in tools
	if e.registry != nil {
		for _, def := range e.registry.Definitions() {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			})
		}
	}

	// MCP tools
	if e.mcpManager != nil {
		for _, t := range e.mcpManager.AllTools() {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        fmt.Sprintf("mcp_%s_%s", t.Server, t.Tool.Name),
				Description: fmt.Sprintf("[MCP:%s] %s", t.Server, t.Tool.Description),
				Parameters:  t.Tool.InputSchema,
			})
		}
	}

	return toolDefs
}

// checkSkillActivation checks if response requests a skill.
func (e *Executor) checkSkillActivation(content string) *skills.Skill {
	re := regexp.MustCompile(`\[use-skill:([a-z0-9-]+)\]`)
	matches := re.FindStringSubmatch(content)
	if len(matches) < 2 {
		return nil
	}

	skillName := matches[1]

	// Check if already loaded
	if skill, ok := e.loadedSkills[skillName]; ok {
		return skill
	}

	// Find and load skill
	for _, ref := range e.skillRefs {
		if ref.Name == skillName {
			skill, err := skills.Load(ref.Path)
			if err != nil {
				return nil
			}
			e.loadedSkills[skillName] = skill
			if e.OnSkillLoaded != nil {
				e.OnSkillLoaded(skillName)
			}
			return skill
		}
	}

	return nil
}

// getSkillContext returns the context to inject for a skill.
func (e *Executor) getSkillContext(skill *skills.Skill) string {
	context := fmt.Sprintf("# Skill: %s\n\n%s", skill.Name, skill.Instructions)

	// List available scripts
	scripts, _ := skill.ListScripts()
	if len(scripts) > 0 {
		context += "\n\n## Available Scripts\n"
		for _, s := range scripts {
			context += fmt.Sprintf("- %s\n", s)
		}
	}

	return context
}

// executeMultiAgentGoal executes a goal with multiple sub-agents.
// Each agent runs in complete isolation with its own tools, memory, and context.
func (e *Executor) executeMultiAgentGoal(ctx context.Context, goal *agentfile.Goal) (string, error) {
	// Collect agent definitions
	var agents []*agentfile.Agent
	for _, agentName := range goal.UsingAgent {
		agent := e.findAgent(agentName)
		if agent == nil {
			return "", fmt.Errorf("agent not found: %s", agentName)
		}
		agents = append(agents, agent)
	}

	// Check if we have a sub-agent runner
	if e.subAgentRunner != nil {
		return e.executeWithSubAgentRunner(ctx, goal, agents)
	}

	// Fallback: simple parallel execution without true isolation
	// (for backwards compatibility when no package paths configured)
	return e.executeSimpleParallel(ctx, goal, agents)
}

// executeWithSubAgentRunner uses the sub-agent runner for true isolated execution.
func (e *Executor) executeWithSubAgentRunner(ctx context.Context, goal *agentfile.Goal, agents []*agentfile.Agent) (string, error) {
	// Build input from current state
	input := make(map[string]string)
	for k, v := range e.inputs {
		input[k] = v
	}
	for k, v := range e.outputs {
		input[k] = v
	}
	// Add the goal outcome as task
	input["_task"] = e.interpolate(goal.Outcome)

	// Spawn all agents in parallel
	results, err := e.subAgentRunner.SpawnParallel(ctx, agents, input)
	if err != nil {
		return "", fmt.Errorf("sub-agent execution failed: %w", err)
	}

	// Collect outputs (with structured parsing if agents have outputs declared)
	agentOutputs := make(map[string]map[string]string)
	var agentOutputStrings []string
	for _, result := range results {
		if result.Error != nil {
			return "", fmt.Errorf("sub-agent %s failed: %w", result.Name, result.Error)
		}

		// Find the agent to check for structured outputs
		agent := e.findAgent(result.Name)
		if agent != nil && len(agent.Outputs) > 0 {
			// Parse structured output from agent
			parsed, err := parseStructuredOutput(result.Output, agent.Outputs)
			if err == nil {
				agentOutputs[result.Name] = parsed
				// Build formatted output for synthesis
				var formatted strings.Builder
				formatted.WriteString(fmt.Sprintf("[%s]:\n", result.Name))
				for field, value := range parsed {
					formatted.WriteString(fmt.Sprintf("- %s: %s\n", field, value))
				}
				agentOutputStrings = append(agentOutputStrings, formatted.String())
			} else {
				// Fallback to raw output
				agentOutputStrings = append(agentOutputStrings, fmt.Sprintf("[%s]: %s", result.Name, result.Output))
			}
		} else {
			agentOutputStrings = append(agentOutputStrings, fmt.Sprintf("[%s]: %s", result.Name, result.Output))
		}
	}

	// If single agent, return directly
	if len(results) == 1 {
		output := results[0].Output
		// Store structured outputs if parsed
		if parsed, ok := agentOutputs[results[0].Name]; ok {
			for field, value := range parsed {
				e.outputs[field] = value
			}
		}
		if e.OnGoalComplete != nil {
			e.OnGoalComplete(goal.Name, output)
		}
		return output, nil
	}

	// Multiple agents: synthesize responses
	synthesisPrompt := fmt.Sprintf(
		"Synthesize these agent responses into a coherent answer:\n\n%s",
		strings.Join(agentOutputStrings, "\n\n"),
	)

	// Add structured output instruction for synthesis if goal has outputs
	if len(goal.Outputs) > 0 {
		synthesisPrompt += "\n\n" + buildStructuredOutputInstruction(goal.Outputs)
	}

	messages := []llm.Message{
		{Role: "system", Content: "You are synthesizing multiple agent responses."},
		{Role: "user", Content: synthesisPrompt},
	}

	resp, err := e.provider.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})
	if err != nil {
		if e.OnLLMError != nil {
			e.OnLLMError(err)
		}
		return "", err
	}

	// Parse structured output from synthesis if goal has outputs
	if len(goal.Outputs) > 0 {
		parsed, err := parseStructuredOutput(resp.Content, goal.Outputs)
		if err == nil {
			for field, value := range parsed {
				e.outputs[field] = value
			}
		}
	}

	if e.OnGoalComplete != nil {
		e.OnGoalComplete(goal.Name, resp.Content)
	}

	return resp.Content, nil
}

// executeSimpleParallel executes agents in parallel without true isolation.
// Used as fallback when sub-agent runner is not configured.
func (e *Executor) executeSimpleParallel(ctx context.Context, goal *agentfile.Goal, agents []*agentfile.Agent) (string, error) {
	type agentResult struct {
		name   string
		output string
		err    error
	}

	resultChan := make(chan agentResult, len(agents))
	var wg sync.WaitGroup

	for _, agent := range agents {
		wg.Add(1)
		go func(agent *agentfile.Agent) {
			defer wg.Done()

			// Get provider for this agent's profile
			provider, err := e.providerFactory.GetProvider(agent.Requires)
			if err != nil {
				resultChan <- agentResult{name: agent.Name, err: err}
				return
			}

			prompt := e.interpolate(goal.Outcome)

			// Use agent's prompt as system message, or generic if none
			systemPrompt := "You are a helpful assistant."
			if agent.Prompt != "" {
				systemPrompt = agent.Prompt
			}

			messages := []llm.Message{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: prompt},
			}

			resp, err := provider.Chat(ctx, llm.ChatRequest{
				Messages: messages,
			})

			if err != nil {
				resultChan <- agentResult{name: agent.Name, err: err}
				return
			}
			resultChan <- agentResult{name: agent.Name, output: resp.Content}
		}(agent)
	}

	wg.Wait()
	close(resultChan)

	// Collect results
	var agentOutputs []string
	for result := range resultChan {
		if result.err != nil {
			return "", result.err
		}
		agentOutputs = append(agentOutputs, fmt.Sprintf("[%s]: %s", result.name, result.output))
	}

	// Synthesize responses
	synthesisPrompt := fmt.Sprintf(
		"Synthesize these agent responses into a coherent answer:\n\n%s",
		strings.Join(agentOutputs, "\n\n"),
	)

	messages := []llm.Message{
		{Role: "system", Content: "You are synthesizing multiple agent responses."},
		{Role: "user", Content: synthesisPrompt},
	}

	resp, err := e.provider.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})
	if err != nil {
		if e.OnLLMError != nil {
			e.OnLLMError(err)
		}
		return "", err
	}

	if e.OnGoalComplete != nil {
		e.OnGoalComplete(goal.Name, resp.Content)
	}

	return resp.Content, nil
}

// executeTool executes a tool call (built-in or MCP).
func (e *Executor) executeTool(ctx context.Context, tc llm.ToolCallResponse) (interface{}, error) {
	start := time.Now()

	// Security verification before execution
	if err := e.verifyToolCall(ctx, tc.Name, tc.Args); err != nil {
		e.logToolResult(tc.Name, nil, err, time.Since(start))
		if e.OnToolError != nil {
			e.OnToolError(tc.Name, tc.Args, err)
		}
		return nil, err
	}

	// Log the tool call
	e.logToolCall(tc.Name, tc.Args)

	// Check if it's an MCP tool
	if strings.HasPrefix(tc.Name, "mcp_") {
		result, err := e.executeMCPTool(ctx, tc)
		duration := time.Since(start)
		e.logToolResult(tc.Name, result, err, duration)

		// MCP tools return external content - register as untrusted
		if err == nil && result != nil {
			e.registerUntrustedResult(tc.Name, result)
		}
		return result, err
	}

	// Built-in tool
	if e.registry == nil {
		return nil, fmt.Errorf("no tool registry")
	}

	tool := e.registry.Get(tc.Name)
	if tool == nil {
		return nil, fmt.Errorf("tool not found: %s", tc.Name)
	}

	result, err := tool.Execute(ctx, tc.Args)
	duration := time.Since(start)

	// Log the tool result
	e.logToolResult(tc.Name, result, err, duration)

	// Register external tool results as untrusted content
	if err == nil && result != nil && isExternalTool(tc.Name) {
		e.registerUntrustedResult(tc.Name, result)
	}

	if err != nil && e.OnToolError != nil {
		e.OnToolError(tc.Name, tc.Args, err)
	}

	if e.OnToolCall != nil {
		e.OnToolCall(tc.Name, tc.Args, result)
	}

	return result, err
}

// isExternalTool returns true if the tool fetches external/untrusted content.
func isExternalTool(name string) bool {
	externalTools := map[string]bool{
		"web_fetch":  true,
		"web_search": true,
	}
	return externalTools[name]
}

// registerUntrustedResult registers tool result as untrusted content block.
func (e *Executor) registerUntrustedResult(toolName string, result interface{}) {
	if e.securityVerifier == nil {
		return
	}

	// Convert result to string for block registration
	var content string
	switch v := result.(type) {
	case string:
		content = v
	case []byte:
		content = string(v)
	default:
		// JSON serialize complex results
		if data, err := json.Marshal(v); err == nil {
			content = string(data)
		} else {
			content = fmt.Sprintf("%v", v)
		}
	}

	// Skip empty results
	if content == "" || content == "null" {
		return
	}

	// Register as untrusted content block
	source := fmt.Sprintf("tool:%s", toolName)
	e.AddUntrustedContent(content, source)
}

// toolResult holds the result of a parallel tool execution.
type toolResult struct {
	index   int
	id      string
	content string
}

// executeToolsParallel executes multiple tool calls concurrently and returns
// messages in the original order.
func (e *Executor) executeToolsParallel(ctx context.Context, toolCalls []llm.ToolCallResponse) []llm.Message {
	if len(toolCalls) == 0 {
		return nil
	}

	// For single tool call, no need for goroutines
	if len(toolCalls) == 1 {
		tc := toolCalls[0]
		result, err := e.executeTool(ctx, tc)
		var content string
		if err != nil {
			content = fmt.Sprintf("Error: %v", err)
		} else {
			switch v := result.(type) {
			case string:
				content = v
			default:
				data, _ := json.Marshal(v)
				content = string(data)
			}
		}
		return []llm.Message{{
			Role:       "tool",
			ToolCallID: tc.ID,
			Content:    content,
		}}
	}

	// Execute tools in parallel
	results := make(chan toolResult, len(toolCalls))
	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		wg.Add(1)
		go func(idx int, tc llm.ToolCallResponse) {
			defer wg.Done()
			result, err := e.executeTool(ctx, tc)
			var content string
			if err != nil {
				content = fmt.Sprintf("Error: %v", err)
			} else {
				switch v := result.(type) {
				case string:
					content = v
				default:
					data, _ := json.Marshal(v)
					content = string(data)
				}
			}
			results <- toolResult{index: idx, id: tc.ID, content: content}
		}(i, tc)
	}

	// Wait for all to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results and sort by original index
	collected := make([]toolResult, 0, len(toolCalls))
	for r := range results {
		collected = append(collected, r)
	}

	// Sort by original order
	messages := make([]llm.Message, len(toolCalls))
	for _, r := range collected {
		messages[r.index] = llm.Message{
			Role:       "tool",
			ToolCallID: r.id,
			Content:    r.content,
		}
	}

	return messages
}

// executeMCPTool executes an MCP tool call.
func (e *Executor) executeMCPTool(ctx context.Context, tc llm.ToolCallResponse) (interface{}, error) {
	if e.mcpManager == nil {
		return nil, fmt.Errorf("no MCP manager configured")
	}

	// Parse tool name: mcp_<server>_<tool>
	parts := strings.SplitN(strings.TrimPrefix(tc.Name, "mcp_"), "_", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid MCP tool name: %s", tc.Name)
	}

	server, toolName := parts[0], parts[1]

	// Check MCP tool policy
	if e.policy != nil {
		allowed, reason, warning := e.policy.CheckMCPTool(server, toolName)
		if warning != "" {
			e.logger.SecurityWarning(warning, map[string]interface{}{
				"server": server,
				"tool":   toolName,
			})
		}
		if !allowed {
			return nil, fmt.Errorf("policy denied: %s", reason)
		}
	}

	result, err := e.mcpManager.CallTool(ctx, server, toolName, tc.Args)
	if err != nil {
		return nil, err
	}

	if e.OnMCPToolCall != nil {
		e.OnMCPToolCall(server, toolName, tc.Args, result)
	}

	// Extract text content
	var output strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" {
			output.WriteString(c.Text)
		}
	}

	return output.String(), nil
}

// interpolate replaces $variables in text.
func (e *Executor) interpolate(text string) string {
	// Replace input variables
	for name, value := range e.inputs {
		text = strings.ReplaceAll(text, "$"+name, value)
	}

	// Replace goal output variables
	for name, value := range e.outputs {
		text = strings.ReplaceAll(text, "$"+name, value)
	}

	// Handle any remaining $var patterns (leave as-is or empty)
	re := regexp.MustCompile(`\$([a-zA-Z_][a-zA-Z0-9_]*)`)
	text = re.ReplaceAllStringFunc(text, func(match string) string {
		varName := strings.TrimPrefix(match, "$")
		if val, ok := e.inputs[varName]; ok {
			return val
		}
		if val, ok := e.outputs[varName]; ok {
			return val
		}
		return match // Leave unresolved variables as-is
	})

	return text
}

// findGoal finds a goal by name.
func (e *Executor) findGoal(name string) *agentfile.Goal {
	for i := range e.workflow.Goals {
		if e.workflow.Goals[i].Name == name {
			return &e.workflow.Goals[i]
		}
	}
	return nil
}

// findAgent finds an agent by name.
func (e *Executor) findAgent(name string) *agentfile.Agent {
	for i := range e.workflow.Agents {
		if e.workflow.Agents[i].Name == name {
			return &e.workflow.Agents[i]
		}
	}
	return nil
}

// buildStructuredOutputInstruction creates the instruction for structured JSON output.
func buildStructuredOutputInstruction(outputs []string) string {
	var sb strings.Builder
	sb.WriteString("Respond with a JSON object containing these fields:\n")
	for _, field := range outputs {
		sb.WriteString(fmt.Sprintf("- %s\n", field))
	}
	sb.WriteString("\nProvide only the JSON object, no additional text.")
	return sb.String()
}

// parseStructuredOutput extracts fields from JSON response.
func parseStructuredOutput(content string, expectedFields []string) (map[string]string, error) {
	// Try to find JSON in the response (it might be wrapped in markdown code blocks)
	jsonStr := extractJSON(content)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in response")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	result := make(map[string]string)
	for _, field := range expectedFields {
		if val, ok := parsed[field]; ok {
			// Convert value to string
			switch v := val.(type) {
			case string:
				result[field] = v
			case []interface{}:
				// Convert array to JSON string
				bytes, _ := json.Marshal(v)
				result[field] = string(bytes)
			default:
				// Convert other types to JSON
				bytes, _ := json.Marshal(v)
				result[field] = string(bytes)
			}
		}
	}

	return result, nil
}

// extractJSON finds and returns JSON object from text that may contain markdown or other content.
func extractJSON(content string) string {
	// First try: look for ```json code block
	jsonBlockRe := regexp.MustCompile("(?s)```json\\s*\\n?(.*?)\\n?```")
	if matches := jsonBlockRe.FindStringSubmatch(content); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// Second try: look for ``` code block (no language specified)
	codeBlockRe := regexp.MustCompile("(?s)```\\s*\\n?(.*?)\\n?```")
	if matches := codeBlockRe.FindStringSubmatch(content); len(matches) > 1 {
		candidate := strings.TrimSpace(matches[1])
		if strings.HasPrefix(candidate, "{") {
			return candidate
		}
	}

	// Third try: find raw JSON object
	start := strings.Index(content, "{")
	if start == -1 {
		return ""
	}

	// Find matching closing brace
	depth := 0
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1]
			}
		}
	}

	return ""
}
