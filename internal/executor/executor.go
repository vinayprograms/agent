// Package executor orchestrates workflow execution: parsing goals, dispatching
// LLM calls, running tools, and coordinating sub-agents.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/hooks"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/skills"
	"github.com/vinayprograms/agent/internal/step"
	"github.com/vinayprograms/agent/internal/supervision"
	"github.com/vinayprograms/agentkit/contentguard"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/mcp"
	"github.com/vinayprograms/agentkit/memory"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/tools"
)

// MetricsCollector receives LLM and supervision metrics for heartbeat reporting.
type MetricsCollector interface {
	RecordLLMCall(inputTokens, outputTokens, cacheCreation, cacheRead int, latencyMs int64)
	RecordSupervision(approved bool)
	SetSubagents(count int)
}

// ObservationExtractor extracts findings, insights and lessons from step
// outputs. *memory.Extractor satisfies it.
type ObservationExtractor interface {
	Extract(ctx context.Context, text string, opts ...memory.ExtractOption) (findings, insights, lessons []string, err error)
}

// ObservationStore persists extracted observations. memory stores
// (*memory.BleveStore, *memory.InMemoryStore) satisfy it.
type ObservationStore interface {
	RememberFIL(ctx context.Context, findings, insights, lessons []string, source string) ([]string, error)
}

// singleResolver resolves every profile to one model.
type singleResolver struct{ m llm.Model }

func (s singleResolver) Model(string) (llm.Model, error) { return s.m, nil }

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

type Status string

const (
	StatusRunning  Status = "running"
	StatusComplete Status = "complete"
	StatusFailed   Status = "failed"
)

type Result struct {
	Status     Status
	Outputs    map[string]string
	Iterations map[string]int
	Error      string
}

// Executor is the central orchestrator: it runs the LLM loop, dispatches
// tool calls, manages sub-agents, and coordinates supervision phases.
type Executor struct {
	workflow *agentfile.Workflow
	model    llm.Model    // Default model
	resolver llm.Resolver // Profile-based models
	registry *tools.Registry
	policy   *policy.Policy
	logger   *slog.Logger

	// Debug mode - when true, logs full content (prompts, responses, tool outputs)
	debug bool

	// MCP support
	mcpManager *mcp.Manager

	// Skills support
	skillRefs    []skills.SkillRef
	loadedSkills map[string]*skills.Skill

	// Session logging
	session               *session.Session
	persistentSession     bool // When true, Run() does not close the session (serve mode)
	currentGoal           string
	currentGoalSupervised bool // Whether the current goal is supervised (inherited by sub-agents)

	// State
	inputs  map[string]string
	outputs map[string]string

	// Supervision support
	checkpointStore supervision.Store
	supervisor      supervision.Supervisor
	humanAvailable  bool
	humanInputChan  chan string

	// Hooks for cross-cutting concerns (logging, telemetry, metrics).
	// Multiple listeners can subscribe to each event type.
	hooks *hooks.Registry

	// Content guard (nil = tool-call verification disabled)
	guard *contentguard.Guard

	// Timeouts for network operations (seconds)
	timeoutMCP       int
	timeoutWebSearch int
	timeoutWebFetch  int

	// Goal timing tracking
	goalStartTimes map[string]time.Time

	// Security research context (for defensive framing in prompts)
	securityResearchScope string

	// Observation extraction for semantic memory
	observationExtractor ObservationExtractor
	observationStore     ObservationStore

	// Convergence tracking
	convergenceFailures map[string]int // goals that hit WITHIN limit without converging
	convergenceContext  string         // current convergence history (for multi-agent goals)
	mu                  sync.Mutex     // protects convergenceFailures

	// Metrics collector for heartbeat reporting (optional, set by serve mode)
	metricsCollector MetricsCollector

	// Sub-agent tracking
	activeSubAgents atomic.Int32

	// Interrupt buffer for swarm collaboration (nil = non-swarm mode)
	interruptBuffer *InterruptBuffer

	// Discuss publisher for swarm collaboration (nil = non-swarm mode).
	// Called with each non-tool-call LLM response during execution.
	// The caller (serve.go) binds the task ID in the closure.
	discussPublisher func(goalName, content string)

	// Workspace context injected into system prompt so the agent
	// knows the project layout without needing to discover it.
	workspaceContext string

	// Supervision pipeline for the four-phase flow (COMMIT->EXECUTE->RECONCILE->SUPERVISE).
	pipeline *supervision.Pipeline

	// background owns every fire-and-forget goroutine the executor starts
	// (observation extraction, async tools). Run waits on it before the
	// session is flushed or closed, so no goroutine can log to — or block
	// on — a session that is already shut down.
	background sync.WaitGroup
}

// phaseLoggerAdapter adapts the Executor's logging methods to the supervision.PhaseLogger interface.
type phaseLoggerAdapter struct {
	e *Executor
}

func (a *phaseLoggerAdapter) LogPhaseReconcile(goal, stepID string, triggers []string, escalate bool, durationMs int64) {
	a.e.logPhaseReconcile(goal, stepID, triggers, escalate, durationMs)
}

func (a *phaseLoggerAdapter) LogPhaseSupervise(goal, stepID, verdict, correction string, humanRequired bool, durationMs int64) {
	a.e.logPhaseSupervise(goal, stepID, verdict, correction, humanRequired, durationMs)
}

func (a *phaseLoggerAdapter) LogCheckpoint(checkpointType, goal, step, checkpointID string) {
	a.e.logCheckpoint(checkpointType, goal, step, checkpointID)
}

// Registry returns the tool registry.
func (e *Executor) Registry() *tools.Registry {
	return e.registry
}

// Hooks returns the hook registry for registering event listeners.
func (e *Executor) Hooks() *hooks.Registry {
	return e.hooks
}

// publishToDiscuss calls the discuss publisher if set.
func (e *Executor) publishToDiscuss(goalName, content string) {
	if e.discussPublisher != nil && content != "" {
		e.discussPublisher(goalName, content)
	}
}

// New creates an Executor from a Config struct. All dependencies are supplied
// up front — no Set* methods needed after construction. It fails when the
// security configuration cannot be honoured (see SecurityConfig) so that a
// misconfiguration never silently disables verification.
func New(cfg Config) (*Executor, error) {
	model := cfg.Model
	resolver := cfg.Resolver

	// Derive model / resolver from each other when only one is supplied.
	if resolver == nil && model != nil {
		resolver = singleResolver{m: model}
	}
	if model == nil && resolver != nil {
		model, _ = resolver.Model("")
	}

	hk := cfg.Hooks
	if hk == nil {
		hk = hooks.NewRegistry()
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "executor")

	e := &Executor{
		workflow:             cfg.Workflow,
		model:                model,
		resolver:             resolver,
		registry:             cfg.Registry,
		policy:               cfg.Policy,
		logger:               logger,
		debug:                cfg.Debug,
		mcpManager:           cfg.MCPManager,
		skillRefs:            cfg.SkillRefs,
		loadedSkills:         make(map[string]*skills.Skill),
		session:              cfg.Session,
		persistentSession:    cfg.PersistentSession,
		outputs:              make(map[string]string),
		checkpointStore:      cfg.CheckpointStore,
		supervisor:           cfg.Supervisor,
		humanAvailable:       cfg.HumanAvailable,
		humanInputChan:       cfg.HumanInputChan,
		hooks:                hk,
		timeoutMCP:           cfg.TimeoutMCP,
		timeoutWebSearch:     cfg.TimeoutWebSearch,
		timeoutWebFetch:      cfg.TimeoutWebFetch,
		goalStartTimes:       make(map[string]time.Time),
		observationExtractor: cfg.ObservationExtractor,
		observationStore:     cfg.ObservationStore,
		metricsCollector:     cfg.MetricsCollector,
		workspaceContext:     cfg.WorkspaceContext,
	}

	if cfg.Security != nil {
		if cfg.Security.Mode == SecurityResearch {
			e.securityResearchScope = cfg.Security.Scope
		}
		guard, err := newContentGuard(cfg.Security, e.registeredToolNames())
		if err != nil {
			return nil, err
		}
		e.guard = guard
	}

	// Build supervision pipeline if both store and supervisor are available.
	if e.supervisor != nil && e.checkpointStore != nil {
		e.pipeline = supervision.NewPipeline(supervision.PipelineConfig{
			Store:      e.checkpointStore,
			Supervisor: e.supervisor,
			Logger:     e.logger,
			Phase:      &phaseLoggerAdapter{e: e},
			// supervision.EventHook carries no context (its signature lives
			// in internal/supervision); hooks only need a non-nil one.
			Event: func(stepID string, phase string, data any) {
				e.hooks.Fire(context.Background(), hooks.SupervisionEvent, map[string]any{
					"step_id": stepID, "phase": phase, "data": data,
				})
			},
		})
	}

	if cfg.SpawnBinder != nil {
		cfg.SpawnBinder.Bind(e.spawnDynamicAgent)
	}
	return e, nil
}

// registeredToolNames lists every tool in the registry.
func (e *Executor) registeredToolNames() []string {
	if e.registry == nil {
		return nil
	}
	defs := e.registry.Definitions()
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	return names
}

// recordLLMMetrics reports token usage and latency to the metrics collector.
func (e *Executor) recordLLMMetrics(resp *llm.ChatResponse, latency time.Duration) {
	if e.metricsCollector == nil || resp == nil {
		return
	}
	e.metricsCollector.RecordLLMCall(
		resp.InputTokens, resp.OutputTokens,
		resp.CacheCreationInputTokens, resp.CacheReadInputTokens,
		latency.Milliseconds(),
	)
}

// extractAndStoreObservations extracts observations from step output and stores them.
func (e *Executor) extractAndStoreObservations(ctx context.Context, stepName, stepType, output string) {
	if e.observationExtractor == nil || e.observationStore == nil {
		return
	}

	// Extraction runs off the critical path but is owned by the executor:
	// Run waits for it. Cancellation is detached — a half-stored
	// observation set is worse than a slightly late one.
	source := stepType + ":" + stepName
	ctx = context.WithoutCancel(ctx)
	e.background.Go(func() {
		f, i, l, err := e.observationExtractor.Extract(ctx, output, memory.WithSource(source))
		if err != nil || len(f)+len(i)+len(l) == 0 {
			return
		}
		if _, err := e.observationStore.RememberFIL(ctx, f, i, l, source); err != nil {
			e.logger.Warn("failed to store observations", "source", source, "error", err.Error())
		}
	})
}

// flushSession flushes buffered session events to disk.
func (e *Executor) flushSession() {
	if e.session != nil {
		e.session.Flush()
	}
}

// closeSession stops the session writer, flushing all remaining events.
func (e *Executor) closeSession() {
	if e.session != nil {
		e.session.Close()
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

// RunOptions carries the state of a single workflow run. Swarm callers
// (serve mode) create a fresh one per task instead of mutating a shared
// executor: Interrupts and Discuss apply to this run only.
type RunOptions struct {
	// Inputs binds the workflow's declared inputs. Missing inputs fall
	// back to their defaults; an input with neither is an error.
	Inputs map[string]string
	// Interrupts is drained between LLM turns and injected into the
	// context. nil disables interrupts.
	Interrupts *InterruptBuffer
	// Discuss receives every non-tool-call LLM response, for publishing to
	// the swarm discuss channel. nil disables publishing.
	Discuss func(goalName, content string)
}

// Run executes the workflow: binds inputs, builds the step graph, and runs it.
// It returns only after every goroutine it started has finished.
func (e *Executor) Run(ctx context.Context, opts RunOptions) (*Result, error) {
	e.interruptBuffer = opts.Interrupts
	e.discussPublisher = opts.Discuss
	defer func() {
		e.interruptBuffer = nil
		e.discussPublisher = nil
	}()

	startTime := time.Now()
	workflowName := e.workflow.Name
	if workflowName == "" {
		workflowName = "unnamed"
	}
	e.logger.Info("execution_start", "workflow", workflowName)

	// Start workflow span
	ctx, workflowSpan := e.startWorkflowSpan(ctx, workflowName)

	// Flush or close session at end of workflow execution.
	// Persistent sessions (serve mode) stay open across tasks.
	if e.persistentSession {
		defer e.flushSession()
	} else {
		defer e.closeSession()
	}
	// Runs after the session defers are registered, therefore before them:
	// background work still has an open session to write to.
	defer e.background.Wait()

	// Set main agent identity in context (workflow name as both name and role)
	ctx = withAgentIdentity(ctx, workflowName, "main")

	// Pre-flight check for SUPERVISED HUMAN requirements
	if err := e.PreFlight(); err != nil {
		e.logExecutionComplete(workflowName, startTime, string(StatusFailed))
		e.endWorkflowSpan(workflowSpan, string(StatusFailed), err)
		return &Result{Status: StatusFailed, Error: err.Error()}, err
	}

	// Bind inputs
	if err := e.bindInputs(opts.Inputs); err != nil {
		e.logExecutionComplete(workflowName, startTime, string(StatusFailed))
		e.endWorkflowSpan(workflowSpan, string(StatusFailed), err)
		return &Result{Status: StatusFailed, Error: err.Error()}, err
	}

	// Build and execute the step graph
	graph := step.BuildGraph(e.workflow, e)
	state := step.NewState(e.inputs)

	if err := graph.Execute(ctx, state); err != nil {
		e.logExecutionComplete(workflowName, startTime, string(StatusFailed))
		e.endWorkflowSpan(workflowSpan, string(StatusFailed), err)
		return &Result{Status: StatusFailed, Error: err.Error()}, err
	}

	// Collect outputs
	result := &Result{
		Status:     StatusComplete,
		Outputs:    state.Outputs,
		Iterations: e.GetConvergenceFailures(),
	}
	e.logExecutionComplete(workflowName, startTime, string(StatusComplete))
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

// ExecuteGoal executes a single goal by name, updating the given state.
// This implements step.GoalExecutor.
func (e *Executor) ExecuteGoal(ctx context.Context, goalName string, state *step.State) error {
	goal := e.findGoal(goalName)
	if goal == nil {
		return fmt.Errorf("goal %q not found in workflow", goalName)
	}

	// Sync state from step.State to executor's internal state
	for k, v := range state.Inputs {
		if _, exists := e.inputs[k]; !exists {
			e.inputs[k] = v
		}
	}
	for k, v := range state.Outputs {
		e.outputs[k] = v
	}

	result, err := e.executeGoalWithTracking(ctx, goal)
	if err != nil {
		return err
	}

	// Store output in step state
	state.Outputs[goalName] = result.Output
	e.outputs[goalName] = result.Output

	return nil
}

// GoalResult contains the result of executing a goal.
type GoalResult struct {
	Output        string
	ToolCallsMade bool
}

// getPipeline returns the supervision pipeline. If no pipeline is configured
// (supervision not enabled), it returns a pipeline with no supervisor/store,
// which will simply pass through to the execute function.
func (e *Executor) getPipeline() *supervision.Pipeline {
	if e.pipeline != nil {
		return e.pipeline
	}
	// Return a passthrough pipeline (no store/supervisor means it just executes)
	return supervision.NewPipeline(supervision.PipelineConfig{
		Logger: e.logger,
	})
}

// isSupervised determines if a goal should be supervised based on goal settings and workflow defaults.
func (e *Executor) isSupervised(goal *agentfile.Goal) bool {
	return goal.IsSupervised(e.workflow)
}

// requiresHuman determines if a goal requires human approval.
func (e *Executor) requiresHuman(goal *agentfile.Goal) bool {
	return goal.RequiresHuman(e.workflow)
}

// goalOutcome returns the interpolated outcome description for a goal by name, falling back to the name itself.
// Handles goals defined inline, from a file (FROM path.md), or from an agent skill.
func (e *Executor) goalOutcome(name string) string {
	for _, g := range e.workflow.Goals {
		if g.Name == name {
			if g.Outcome != "" {
				return e.interpolate(g.Outcome)
			}
			break
		}
	}
	return name
}

// executeGoalWithTracking executes a single goal with four-phase execution when supervision is enabled.
// Phases: COMMIT -> EXECUTE -> RECONCILE -> SUPERVISE
// All steps capture checkpoints; only supervised steps run RECONCILE/SUPERVISE.
func (e *Executor) executeGoalWithTracking(ctx context.Context, goal *agentfile.Goal) (*GoalResult, error) {
	// Log goal start
	e.logGoalStart(goal.Name)

	e.hooks.Fire(ctx, hooks.GoalStart, map[string]any{"name": goal.Name})

	// Check for convergence goal
	if goal.IsConverge {
		result, err := e.executeConvergeGoal(ctx, goal)
		if err != nil {
			return nil, err
		}
		// Parse structured output if declared
		if len(goal.Outputs) > 0 {
			parsedOutputs, err := parseStructuredOutput(result.Output, goal.Outputs)
			if err != nil {
				e.logEvent(session.EventSystem, fmt.Sprintf("Warning: failed to parse structured output: %v", err))
			} else {
				for field, value := range parsedOutputs {
					e.outputs[field] = value
				}
			}
		}
		e.logGoalEnd(goal.Name, result.Output)
		e.flushSession()
		return &GoalResult{Output: result.Output, ToolCallsMade: false}, nil
	}

	// Check for multi-agent execution
	if len(goal.UsingAgent) > 0 {
		output, err := e.executeMultiAgentGoal(ctx, goal)
		if err != nil {
			return nil, err
		}
		// Parse structured output if declared (same as regular goals)
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
		e.logGoalEnd(goal.Name, output)
		e.flushSession()
		return &GoalResult{Output: output, ToolCallsMade: false}, nil
	}

	// Build XML-structured prompt with context from previous goals
	b := newBrief(e.workflow.Name)

	// Add prior goal outputs to context
	for goalName, output := range e.outputs {
		b.AddPriorGoal(goalName, output)
	}

	// Set current goal with interpolated description
	goalDescription := e.interpolate(goal.Outcome)

	// Add structured output instruction if outputs are declared
	if len(goal.Outputs) > 0 {
		goalDescription += "\n\n" + buildStructuredOutputInstruction(goal.Outputs)
	}

	b.SetCurrentGoal(goal.Name, goalDescription)

	// Build the XML prompt
	prompt := b.String()

	// Set current goal for logging
	e.currentGoal = goal.Name

	// Determine supervision status
	supervised := e.isSupervised(goal)
	humanRequired := e.requiresHuman(goal)

	// Track supervision status for sub-agents spawned during this goal
	e.currentGoalSupervised = supervised

	// Run through the supervision pipeline (or just execute if unsupervised)
	pipelineResult, err := e.getPipeline().Run(
		ctx,
		supervision.PipelineRequest{
			StepID:        goal.Name,
			GoalName:      goalDescription,
			Supervised:    supervised,
			HumanRequired: humanRequired,
		},
		supervision.Work{
			// COMMIT: declare intent
			Commit: func(ctx context.Context) *checkpoint.PreCheckpoint {
				return e.commitPhase(ctx, goal, prompt)
			},
			// EXECUTE: do the work
			Execute: func(ctx context.Context) (*supervision.ExecuteResult, error) {
				output, toolsUsed, toolCallsMade, err := e.executePhase(ctx, goal, prompt)
				return &supervision.ExecuteResult{Output: output, ToolsUsed: toolsUsed, ToolCallsMade: toolCallsMade}, err
			},
			// POST-CHECKPOINT: self-assessment
			Post: func(ctx context.Context, pre *checkpoint.PreCheckpoint, output string, toolsUsed []string) *checkpoint.PostCheckpoint {
				return e.createPostCheckpoint(ctx, goal, pre, output, toolsUsed)
			},
		},
	)
	if err != nil {
		return nil, err
	}

	output := pipelineResult.Output
	toolCallsMade := pipelineResult.ToolCallsMade

	// Handle supervision verdict
	switch pipelineResult.Verdict {
	case supervision.VerdictReorient:
		e.logger.Info("reorienting execution", "goal", goal.Name, "correction", pipelineResult.Correction)
		b.SetCorrection(pipelineResult.Correction)
		correctedPrompt := b.String()
		output, _, toolCallsMade, err = e.executePhase(ctx, goal, correctedPrompt)
		if err != nil {
			return nil, err
		}

	case supervision.VerdictPause:
		return nil, fmt.Errorf("supervision paused but no resolution provided")
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

	e.hooks.Fire(ctx, hooks.GoalComplete, map[string]any{"name": goal.Name, "output": output})
	e.extractAndStoreObservations(ctx, goal.Name, "GOAL", output)
	e.logGoalEnd(goal.Name, output)
	e.flushSession()
	return &GoalResult{Output: output, ToolCallsMade: toolCallsMade}, nil
}

// commitPhase asks the agent to declare its intent before execution.
func (e *Executor) commitPhase(ctx context.Context, goal *agentfile.Goal, prompt string) *checkpoint.PreCheckpoint {
	start := time.Now()
	e.logPhaseStart("COMMIT", goal.Name, "")

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

	commitStart := time.Now()
	resp, err := e.model.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})
	e.recordLLMMetrics(resp, time.Since(commitStart))

	pre := &checkpoint.PreCheckpoint{
		StepID:      goal.Name,
		StepType:    "GOAL",
		Instruction: prompt,
		Timestamp:   time.Now(),
	}

	if err != nil {
		e.logger.Warn("commit phase LLM error", "error", err.Error())
		pre.Confidence = "low"
		pre.Assumptions = []string{"Failed to get commitment from agent"}
		e.logPhaseComplete("COMMIT", goal.Name, "", start, "error")
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
	systemMsg := InformationProcessingGuidance + TersenessGuidance + "\nYou are a helpful assistant executing a workflow goal."

	// Inject security research framing if enabled
	if prefix := e.securityResearchPrefix(); prefix != "" {
		systemMsg = prefix + systemMsg
	}

	// If spawn_agents tool is available, inject orchestrator guidance
	if e.registry != nil && e.registry.Has("spawn_agents") {
		systemMsg = OrchestratorSystemPromptPrefix + systemMsg
	}

	// If semantic memory tools are available, inject guidance
	if e.registry != nil && e.registry.Has("recall") {
		systemMsg = SemanticMemoryGuidancePrefix + systemMsg
	}

	// If scratchpad tools are available, inject guidance
	if e.registry != nil && e.registry.Has("scratchpad_write") {
		systemMsg = ScratchpadGuidancePrefix + systemMsg
	}

	// Inject workspace context so the agent knows the project layout
	if e.workspaceContext != "" {
		systemMsg += "\n\n" + e.workspaceContext
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
	e.logger.Debug("tools available", "count", len(toolDefs))

	// Track tools used
	toolsUsedMap := make(map[string]bool)

	// Execute goal loop
	for {
		llmStart := time.Now()
		resp, err := e.model.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		llmDuration := time.Since(llmStart)
		if err != nil {
			e.hooks.Fire(ctx, hooks.LLMError, map[string]any{"error": err})
			e.logPhaseExecute(goal.Name, "error", time.Since(start).Milliseconds())
			return "", nil, toolCallsMade, fmt.Errorf("LLM error: %w", err)
		}

		// Log full LLM interaction (for -vv replay)
		e.logLLMCall(ctx, session.EventAssistant, messages, resp, llmDuration)
		e.recordLLMMetrics(resp, llmDuration)

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

		// No tool calls — publish to discuss and check for pending interrupts
		if len(resp.ToolCalls) == 0 {
			// Publish reasoning to discuss.* for swarm transparency
			e.publishToDiscuss(goal.Name, resp.Content)

			if interrupts := e.interruptBuffer.Drain(); len(interrupts) > 0 {
				// Interrupts arrived — LLM must re-evaluate before terminating
				messages = append(messages, llm.Message{
					Role:    "assistant",
					Content: resp.Content,
				})
				interruptBlock := FormatInterruptsBlock(goal.Outcome, interrupts)
				messages = append(messages, llm.Message{
					Role:    "user",
					Content: interruptBlock,
				})
				e.logEvent(session.EventUser, interruptBlock)
				continue
			}
			// No interrupts, no tool calls — execution complete
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

		// Drain interrupt buffer after tool execution
		if interrupts := e.interruptBuffer.Drain(); len(interrupts) > 0 {
			interruptBlock := FormatInterruptsBlock(goal.Outcome, interrupts)
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: interruptBlock,
			})
			e.logEvent(session.EventUser, interruptBlock)
		}
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

	reconcileStart := time.Now()
	resp, err := e.model.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})
	e.recordLLMMetrics(resp, time.Since(reconcileStart))

	post := &checkpoint.PostCheckpoint{
		StepID:       goal.Name,
		ActualOutput: output,
		ToolsUsed:    toolsUsed,
		Timestamp:    time.Now(),
	}

	if err != nil {
		e.logger.Warn("post-checkpoint LLM error", "error", err.Error())
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
// Registry definitions are filtered by policy: tools disabled by policy are
// not advertised to the model.
func (e *Executor) getAllToolDefinitions() []llm.ToolDef {
	var toolDefs []llm.ToolDef

	// Built-in tools
	if e.registry != nil {
		for _, def := range e.registry.Definitions() {
			if e.policy != nil && !e.policy.IsToolEnabled(def.Name) {
				continue
			}
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.JSONSchema(),
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

// executeMultiAgentGoal executes a goal that uses multiple agents in parallel.
// Applies the four-phase checkpoint model at the goal level.
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

	// Set current goal for logging and sub-agent inheritance
	e.currentGoal = goal.Name

	// Determine supervision status
	supervised := e.isSupervised(goal)
	e.currentGoalSupervised = supervised

	// Build prompt description for checkpoint
	prompt := fmt.Sprintf("Execute goal %q using agents: %v\nOutcome: %s",
		goal.Name, goal.UsingAgent, e.interpolate(goal.Outcome))

	// ============================================
	// PHASE 1: COMMIT - Declare intent for multi-agent goal
	// ============================================
	var preCheckpoint *checkpoint.PreCheckpoint
	if supervised && e.checkpointStore != nil {
		preCheckpoint = e.commitPhase(ctx, goal, prompt)
		if err := e.checkpointStore.SavePre(preCheckpoint); err != nil {
			e.logger.Warn("failed to save pre-checkpoint", "error", err.Error())
		} else {
			e.logCheckpoint("pre", goal.Name, "", preCheckpoint.StepID)
		}
		e.hooks.Fire(ctx, hooks.SupervisionEvent, map[string]any{"step_id": goal.Name, "phase": "commit", "data": preCheckpoint})
	}

	// ============================================
	// PHASE 2: EXECUTE - Run agents in parallel
	// ============================================
	output, err := e.executeSimpleParallel(ctx, goal, agents)
	if err != nil {
		return "", err
	}

	// Collect tool names from agents for checkpoint (agents used as "tools")
	var toolsUsed []string
	for _, agent := range agents {
		toolsUsed = append(toolsUsed, "agent:"+agent.Name)
	}

	// ============================================
	// PHASE 3 & 4: POST-CHECKPOINT, RECONCILE & SUPERVISE
	// ============================================
	var postCheckpoint *checkpoint.PostCheckpoint
	if e.checkpointStore != nil && preCheckpoint != nil {
		postCheckpoint = e.createPostCheckpoint(ctx, goal, preCheckpoint, output, toolsUsed)
		if err := e.checkpointStore.SavePost(postCheckpoint); err != nil {
			e.logger.Warn("failed to save post-checkpoint", "error", err.Error())
		} else {
			e.logCheckpoint("post", goal.Name, "", postCheckpoint.StepID)
		}
		e.hooks.Fire(ctx, hooks.SupervisionEvent, map[string]any{"step_id": goal.Name, "phase": "execute", "data": postCheckpoint})
	}

	// RECONCILE & SUPERVISE (only for supervised goals)
	if supervised && e.supervisor != nil && preCheckpoint != nil && postCheckpoint != nil {
		humanRequired := e.requiresHuman(goal)

		goalDescription := e.interpolate(goal.Outcome)
		reconcileStart := time.Now()
		reconcileResult := e.supervisor.Reconcile(preCheckpoint, postCheckpoint)
		reconcileDuration := time.Since(reconcileStart).Milliseconds()

		if e.checkpointStore != nil {
			if err := e.checkpointStore.SaveReconcile(reconcileResult); err != nil {
				e.logger.Warn("failed to save reconcile result", "error", err.Error())
			}
		}
		e.logPhaseReconcile(goal.Name, reconcileResult.StepID, reconcileResult.Triggers, reconcileResult.Supervise, reconcileDuration)

		e.hooks.Fire(ctx, hooks.SupervisionEvent, map[string]any{"step_id": goal.Name, "phase": "reconcile", "data": reconcileResult})

		if reconcileResult.Supervise {
			superviseStart := time.Now()
			decisionTrail := e.checkpointStore.Trail()
			superviseResult, superviseErr := e.supervisor.Supervise(
				ctx,
				supervision.SuperviseRequest{
					OriginalGoal:  goalDescription,
					Pre:           preCheckpoint,
					Post:          postCheckpoint,
					Triggers:      reconcileResult.Triggers,
					DecisionTrail: decisionTrail,
					HumanRequired: humanRequired,
				},
			)
			superviseDuration := time.Since(superviseStart).Milliseconds()

			if superviseErr != nil {
				e.logger.Warn("supervision failed", "error", superviseErr.Error())
			} else {
				if e.checkpointStore != nil {
					if err := e.checkpointStore.SaveSupervise(superviseResult); err != nil {
						e.logger.Warn("failed to save supervise result", "error", err.Error())
					}
				}
				e.logPhaseSupervise(goal.Name, superviseResult.StepID, superviseResult.Verdict, superviseResult.Correction, humanRequired, superviseDuration)

				e.hooks.Fire(ctx, hooks.SupervisionEvent, map[string]any{"step_id": goal.Name, "phase": "supervise", "data": superviseResult})

				// Handle supervision verdict
				if superviseResult.Verdict == "PAUSE" {
					return "", fmt.Errorf("supervision paused: %s", superviseResult.Question)
				}
			}
		}
	}

	return output, nil
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

	// If we're in a convergence loop, use the convergence-aware prompt instead
	// This includes the full XML context with convergence history
	if e.convergenceContext != "" {
		task = e.convergenceContext
	}

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
			systemPrompt := e.interpolate(agent.Prompt) // Interpolate $vars in skill/prompt
			if systemPrompt == "" {
				systemPrompt = fmt.Sprintf("You are a %s. Complete the task and return your findings.", role)
			}
			// Prepend terseness guidance
			systemPrompt = InformationProcessingGuidance + TersenessGuidance + systemPrompt

			// Use spawnAgentWithPrompt which shares code with dynamic agents
			// Pass agent's supervision flag - agent is supervised if it has SUPERVISED or inherits from goal
			output, err := e.spawnAgentWithPrompt(ctx, role, systemPrompt, task, agent.Outputs, agent.Requires, priorGoals, agent.IsSupervised(e.workflow))

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
		e.hooks.Fire(ctx, hooks.GoalComplete, map[string]any{"name": goal.Name, "output": output})
		e.extractAndStoreObservations(ctx, goal.Name, "GOAL", output)
		return output, nil
	}

	// Multiple agents: synthesize responses
	synthesisPrompt := fmt.Sprintf(
		"Synthesize these agent responses into a concise, coherent answer. Eliminate redundancy:\n\n%s",
		strings.Join(agentOutputs, "\n\n"),
	)

	messages := []llm.Message{
		{Role: "system", Content: InformationProcessingGuidance + TersenessGuidance + "You are synthesizing multiple agent responses."},
		{Role: "user", Content: synthesisPrompt},
	}

	synthStart := time.Now()
	resp, err := e.model.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})
	e.recordLLMMetrics(resp, time.Since(synthStart))
	if err != nil {
		e.hooks.Fire(ctx, hooks.LLMError, map[string]any{"error": err})
		return "", err
	}

	e.hooks.Fire(ctx, hooks.GoalComplete, map[string]any{"name": goal.Name, "output": resp.Content})
	e.extractAndStoreObservations(ctx, goal.Name, "GOAL", resp.Content)

	return resp.Content, nil
}

// executeTool executes a tool call (built-in or MCP).
