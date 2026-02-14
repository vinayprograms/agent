// Package executor provides workflow and goal execution.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/logging"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/security"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/skills"
	"github.com/vinayprograms/agent/internal/supervision"
	"github.com/vinayprograms/agentkit/tools"
)

// ObservationExtractor extracts observations from step outputs.
type ObservationExtractor interface {
	Extract(ctx context.Context, stepName, stepType, output string) (interface{}, error)
}

// ObservationStore stores and retrieves observations.
type ObservationStore interface {
	StoreObservation(ctx context.Context, obs interface{}) error
	QueryRelevantObservations(ctx context.Context, query string, limit int) ([]interface{}, error)
}

// Context keys for agent identity (thread-safe via context propagation)
type ctxKey int

const (
	ctxKeyAgentName ctxKey = iota
	ctxKeyAgentRole
)

// AgentIdentity holds agent name and role for logging/attribution.
type AgentIdentity struct {
	Name string // Agent name (e.g., "researcher", "writer", or workflow name for main)
	Role string // Agent role (for dynamic sub-agents, same as name; for static agents, defined role)
}

// withAgentIdentity returns a context with agent identity attached.
func withAgentIdentity(ctx context.Context, name, role string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyAgentName, name)
	ctx = context.WithValue(ctx, ctxKeyAgentRole, role)
	return ctx
}

// getAgentIdentity extracts agent identity from context.
func getAgentIdentity(ctx context.Context) AgentIdentity {
	var id AgentIdentity
	if name, ok := ctx.Value(ctxKeyAgentName).(string); ok {
		id.Name = name
	}
	if role, ok := ctx.Value(ctxKeyAgentRole).(string); ok {
		id.Role = role
	}
	return id
}

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

	// Debug mode - when true, logs full content (prompts, responses, tool outputs)
	debug bool

	// MCP support
	mcpManager *mcp.Manager

	// Skills support
	skillRefs    []skills.SkillRef
	loadedSkills map[string]*skills.Skill

	// Session logging
	session        *session.Session
	sessionManager session.SessionManager
	currentGoal    string
	currentGoalSupervised bool // Whether the current goal is supervised (inherited by sub-agents)

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
	OnToolCall         func(name string, args map[string]interface{}, result interface{}, agentRole string)
	OnToolError        func(name string, args map[string]interface{}, err error, agentRole string)
	OnLLMError         func(err error)
	OnSkillLoaded      func(name string)
	OnMCPToolCall      func(server, tool string, args map[string]interface{}, result interface{})
	OnSubAgentStart    func(name string, input map[string]string)
	OnSubAgentComplete func(name string, output string)
	OnSupervisionEvent func(stepID string, phase string, data interface{})

	// Security verifier
	securityVerifier *security.Verifier

	// Last security check result - used to taint tool results with influencing blocks
	lastSecurityRelatedBlocks []string

	// Goal timing tracking
	goalStartTimes map[string]time.Time

	// Security research context (for defensive framing in prompts)
	securityResearchScope string

	// Observation extraction for semantic memory
	observationExtractor ObservationExtractor
	observationStore     ObservationStore
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
		goalStartTimes:  make(map[string]time.Time),
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
		goalStartTimes:  make(map[string]time.Time),
	}
	e.initSpawner()
	return e
}

// SetSecurityVerifier sets the security verifier for tool call verification.
func (e *Executor) SetSecurityVerifier(v *security.Verifier) {
	e.securityVerifier = v
	e.logger.Info("security verifier attached", nil)
}

// SetSecurityResearchScope sets the security research scope for defensive framing.
// When set, system prompts will include context indicating authorized security research.
func (e *Executor) SetSecurityResearchScope(scope string) {
	e.securityResearchScope = scope
}

// SetDebug enables verbose logging of prompts, responses, and tool outputs.
// When disabled (default), content is redacted to prevent PII leakage in production.
func (e *Executor) SetDebug(debug bool) {
	e.debug = debug
}

// SetObservationExtraction enables observation extraction and storage for semantic memory.
func (e *Executor) SetObservationExtraction(extractor ObservationExtractor, store ObservationStore) {
	e.observationExtractor = extractor
	e.observationStore = store
	if extractor != nil && store != nil {
		e.logger.Info("observation extraction enabled", nil)
	}
}

// extractAndStoreObservations extracts observations from step output and stores them.
func (e *Executor) extractAndStoreObservations(ctx context.Context, stepName, stepType, output string) {
	if e.observationExtractor == nil || e.observationStore == nil {
		return
	}

	// Run extraction asynchronously to not block execution
	go func() {
		obs, err := e.observationExtractor.Extract(context.Background(), stepName, stepType, output)
		if err != nil || obs == nil {
			return
		}
		e.observationStore.StoreObservation(context.Background(), obs)
	}()
}

// verifyToolCall checks a tool call against the security verifier if configured.
func (e *Executor) verifyToolCall(ctx context.Context, toolName string, args map[string]interface{}) error {
	// Clear previous related blocks
	e.lastSecurityRelatedBlocks = nil

	if e.securityVerifier == nil {
		return nil // No security verifier configured
	}

	// Use agent role from context for block filtering in multi-agent scenarios
	agentID := getAgentIdentity(ctx)
	agentContext := agentID.Role
	result, err := e.securityVerifier.VerifyToolCall(ctx, toolName, args, e.currentGoal, agentContext)
	if err != nil {
		return fmt.Errorf("security verification error: %w", err)
	}

	// Store related blocks for taint propagation when registering tool results
	if result.Tier1 != nil {
		for _, b := range result.Tier1.RelatedBlocks {
			e.lastSecurityRelatedBlocks = append(e.lastSecurityRelatedBlocks, b.ID)
		}
	}

	// Log security decision to session
	if result.Tier1 != nil {
		blockID := ""
		var relatedBlockIDs []string
		if result.Tier1.Block != nil {
			blockID = result.Tier1.Block.ID
		}
		for _, b := range result.Tier1.RelatedBlocks {
			relatedBlockIDs = append(relatedBlockIDs, b.ID)
		}
		e.logSecurityStatic(toolName, blockID, relatedBlockIDs, result.Tier1.Pass, result.Tier1.Reasons, result.Tier1.SkipReason, result.TaintLineage)
	}

	if result.Tier2 != nil {
		blockID := ""
		if result.Tier1 != nil && result.Tier1.Block != nil {
			blockID = result.Tier1.Block.ID
		}
		// Set skip reason if triage determined benign (not escalating to supervisor)
		skipReason := ""
		if !result.Tier2.Suspicious {
			skipReason = "triage_benign"
		}
		e.logSecurityTriage(toolName, blockID, result.Tier2.Suspicious, "triage", result.Tier2.LatencyMs, result.Tier2.InputTokens, result.Tier2.OutputTokens, skipReason)
	}

	if result.Tier3 != nil {
		blockID := ""
		if result.Tier1 != nil && result.Tier1.Block != nil {
			blockID = result.Tier1.Block.ID
		}
		e.logSecuritySupervisor(toolName, blockID, string(result.Tier3.Verdict), result.Tier3.Reason, "supervisor", result.Tier3.LatencyMs, result.Tier3.InputTokens, result.Tier3.OutputTokens)
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
func (e *Executor) AddUntrustedContent(ctx context.Context, content, source string) {
	e.AddUntrustedContentWithTaint(ctx, content, source, nil)
}

// AddUntrustedContentWithTaint registers untrusted content with explicit taint lineage.
func (e *Executor) AddUntrustedContentWithTaint(ctx context.Context, content, source string, taintedBy []string) {
	if e.securityVerifier == nil {
		return
	}
	// Use agent role from context for block association in multi-agent scenarios
	agentID := getAgentIdentity(ctx)
	agentContext := agentID.Role

	// Get current event sequence for correlation
	var eventSeq uint64
	if e.session != nil {
		eventSeq = e.session.CurrentSeqID()
	}

	block := e.securityVerifier.AddBlockWithTaint(
		security.TrustUntrusted,
		security.TypeData,
		true,
		content,
		source,
		agentContext,
		eventSeq,
		taintedBy, // Parent blocks that influenced this content
	)

	// Log to session with XML representation including taint info
	taintAttr := ""
	if len(taintedBy) > 0 {
		taintAttr = fmt.Sprintf(` tainted-by="%s"`, strings.Join(taintedBy, ","))
	}
	xmlBlock := fmt.Sprintf(`<block id="%s" trust="untrusted" type="data" source="%s" mutable="true" agent="%s"%s>%s</block>`,
		block.ID, source, agentContext, taintAttr, truncateForLog(content, 200))
	entropy := security.ShannonEntropy([]byte(content))
	e.logSecurityBlockWithTaint(block.ID, "untrusted", "data", source, xmlBlock, entropy, taintedBy)
}

// truncateForLog truncates a string for logging purposes.
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
func (e *Executor) Run(ctx context.Context, inputs map[string]string) (*Result, error) {
	startTime := time.Now()
	workflowName := e.workflow.Name
	if workflowName == "" {
		workflowName = "unnamed"
	}
	e.logger.ExecutionStart(workflowName)

	// Start workflow span
	ctx, workflowSpan := e.startWorkflowSpan(ctx, workflowName)
	defer func() {
		// Span will be ended by the return paths below
	}()

	// Set main agent identity in context (workflow name as both name and role)
	ctx = withAgentIdentity(ctx, workflowName, "main")

	// Pre-flight check for SUPERVISED HUMAN requirements
	if err := e.PreFlight(); err != nil {
		e.logger.ExecutionComplete(workflowName, time.Since(startTime), string(StatusFailed))
		e.endWorkflowSpan(workflowSpan, string(StatusFailed), err)
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
		e.endWorkflowSpan(workflowSpan, string(StatusFailed), err)
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
				e.endWorkflowSpan(workflowSpan, string(StatusFailed), err)
				return result, err
			}
		} else if step.Type == agentfile.StepLOOP {
			if err := e.executeLoopStep(ctx, step, result); err != nil {
				result.Status = StatusFailed
				result.Error = err.Error()
				e.logger.ExecutionComplete(workflowName, time.Since(startTime), string(StatusFailed))
				e.endWorkflowSpan(workflowSpan, string(StatusFailed), err)
				return result, err
			}
		}
	}

	result.Status = StatusComplete
	result.Outputs = e.outputs
	e.logger.ExecutionComplete(workflowName, time.Since(startTime), string(StatusComplete))
	e.endWorkflowSpan(workflowSpan, string(StatusComplete), nil)
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
	return goal.IsSupervised(e.workflow)
}

// requiresHuman determines if a goal requires human approval.
func (e *Executor) requiresHuman(goal *agentfile.Goal) bool {
	return goal.RequiresHuman(e.workflow)
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

	// Build XML-structured prompt with context from previous goals
	xmlBuilder := NewXMLContextBuilder(e.workflow.Name)

	// Add prior goal outputs to context
	for goalName, output := range e.outputs {
		xmlBuilder.AddPriorGoal(goalName, output)
	}

	// Set current goal with interpolated description
	goalDescription := e.interpolate(goal.Outcome)

	// Add structured output instruction if outputs are declared
	if len(goal.Outputs) > 0 {
		goalDescription += "\n\n" + buildStructuredOutputInstruction(goal.Outputs)
	}

	xmlBuilder.SetCurrentGoal(goal.Name, goalDescription)

	// Build the XML prompt
	prompt := xmlBuilder.Build()

	// Set current goal for logging
	e.currentGoal = goal.Name

	// Determine supervision status
	supervised := e.isSupervised(goal)
	humanRequired := e.requiresHuman(goal)

	// Track supervision status for sub-agents spawned during this goal
	e.currentGoalSupervised = supervised

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
				// Build corrected prompt with XML correction tag
				xmlBuilder.SetCorrection(superviseResult.Correction)
				correctedPrompt := xmlBuilder.Build()
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
	e.extractAndStoreObservations(ctx, goal.Name, "GOAL", output)
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

	// Inject security research framing if enabled
	if prefix := e.securityResearchPrefix(); prefix != "" {
		systemMsg = prefix + systemMsg
	}

	// If spawn_agent tool is available, inject orchestrator guidance
	if e.registry != nil && e.registry.Has("spawn_agent") {
		systemMsg = OrchestratorSystemPromptPrefix + systemMsg
	}

	// If semantic memory tools are available, inject guidance
	if e.registry != nil && e.registry.Has("memory_recall") {
		systemMsg = SemanticMemoryGuidancePrefix + systemMsg
	}

	// If scratchpad tools are available, inject guidance
	if e.registry != nil && e.registry.Has("scratchpad_write") {
		systemMsg = ScratchpadGuidancePrefix + systemMsg
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
		llmStart := time.Now()
		resp, err := e.provider.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		llmDuration := time.Since(llmStart)
		if err != nil {
			if e.OnLLMError != nil {
				e.OnLLMError(err)
			}
			e.logPhaseExecute(goal.Name, "error", time.Since(start).Milliseconds())
			return "", nil, toolCallsMade, fmt.Errorf("LLM error: %w", err)
		}

		// Log full LLM interaction (for -vv replay)
		e.logLLMCall(ctx, session.EventAssistant, messages, resp, llmDuration)

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

	// Execute agents in parallel with full tool access
	return e.executeSimpleParallel(ctx, goal, agents)
}

// executeSimpleParallel executes AGENT entries in parallel using the same
// execution path as dynamic sub-agents (spawnDynamicAgent).
func (e *Executor) executeSimpleParallel(ctx context.Context, goal *agentfile.Goal, agents []*agentfile.Agent) (string, error) {
	type agentResult struct {
		name       string
		output     string
		err        error
		durationMs int64
	}

	task := e.interpolate(goal.Outcome)

	// Build prior goals context from completed goals
	priorGoals := e.buildPriorGoalsContext()

	resultChan := make(chan agentResult, len(agents))
	var wg sync.WaitGroup

	for _, agent := range agents {
		wg.Add(1)
		go func(agent *agentfile.Agent) {
			defer wg.Done()
			startTime := time.Now()

			// Use agent's prompt as the role/persona, falling back to name
			role := agent.Name
			systemPrompt := agent.Prompt
			if systemPrompt == "" {
				systemPrompt = fmt.Sprintf("You are a %s. Complete the following task thoroughly and return your findings.", role)
			}

			// Use spawnAgentWithPrompt which shares code with dynamic agents
			output, err := e.spawnAgentWithPrompt(ctx, role, systemPrompt, task, agent.Outputs, agent.Requires, priorGoals)

			resultChan <- agentResult{
				name:       agent.Name,
				output:     output,
				err:        err,
				durationMs: time.Since(startTime).Milliseconds(),
			}
		}(agent)
	}

	wg.Wait()
	close(resultChan)

	// Collect results and log sub-agent completions
	var agentOutputs []string
	for result := range resultChan {
		// Find agent for model info
		model := ""
		for _, a := range agents {
			if a.Name == result.name && a.Requires != "" {
				model = a.Requires
				break
			}
		}

		// Log sub-agent completion
		e.logSubAgentEnd(result.name, result.name, model, result.output, result.durationMs, result.err)

		if result.err != nil {
			return "", result.err
		}
		agentOutputs = append(agentOutputs, fmt.Sprintf("[%s]: %s", result.name, result.output))
	}

	// Single agent: return directly
	if len(agentOutputs) == 1 {
		output := agentOutputs[0]
		parts := strings.SplitN(output, "]: ", 2)
		if len(parts) == 2 {
			output = parts[1]
		}
		if e.OnGoalComplete != nil {
			e.OnGoalComplete(goal.Name, output)
		}
		e.extractAndStoreObservations(ctx, goal.Name, "GOAL", output)
		return output, nil
	}

	// Multiple agents: synthesize responses
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
	e.extractAndStoreObservations(ctx, goal.Name, "GOAL", resp.Content)

	return resp.Content, nil
}

// executeTool executes a tool call (built-in or MCP).
