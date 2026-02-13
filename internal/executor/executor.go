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

// SetSecurityResearchScope sets the security research scope for defensive framing.
// When set, system prompts will include context indicating authorized security research.
func (e *Executor) SetSecurityResearchScope(scope string) {
	e.securityResearchScope = scope
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
		e.logSecurityTriage(toolName, blockID, result.Tier2.Suspicious, "triage", 0, skipReason)
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

// LogBashSecurity logs a bash security decision to the session.
// This is called by the bash security checker callback.
func (e *Executor) LogBashSecurity(command, step string, allowed bool, reason string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}

	verdict := "ALLOW"
	if !allowed {
		verdict = "BLOCK"
	}

	content := fmt.Sprintf("[%s] %s: %s", step, verdict, command)
	if reason != "" {
		content += fmt.Sprintf(" | reason: %s", reason)
	}

	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventBashSecurity,
		Goal:      e.currentGoal,
		Content:   content,
		Timestamp: time.Now(),
	})
	e.sessionManager.Update(e.session)

	// Also log to structured logger
	e.logger.Debug("bash security check", map[string]interface{}{
		"step":    step,
		"allowed": allowed,
		"command": command,
		"reason":  reason,
	})
}

// logToolCall logs a tool call event to the session.
// Returns a correlation ID that should be passed to logToolResult.
func (e *Executor) logToolCall(ctx context.Context, name string, args map[string]interface{}) string {
	corrID := fmt.Sprintf("tool-%d", time.Now().UnixNano())
	
	if e.session == nil || e.sessionManager == nil {
		return corrID
	}
	
	// Get agent identity from context (thread-safe for parallel execution)
	agentID := getAgentIdentity(ctx)
	
	e.session.Events = append(e.session.Events, session.Event{
		Type:          session.EventToolCall,
		CorrelationID: corrID,
		Goal:          e.currentGoal,
		Tool:          name,
		Args:          args,
		Agent:         agentID.Name,
		AgentRole:     agentID.Role,
		Timestamp:     time.Now(),
	})
	e.sessionManager.Update(e.session)
	return corrID
}

// logToolResult logs a tool result event to the session.
func (e *Executor) logToolResult(ctx context.Context, name string, args map[string]interface{}, corrID string, result interface{}, err error, duration time.Duration) {
	// Structured logging to stdout
	e.logger.ToolResult(name, duration, err)

	if e.session == nil || e.sessionManager == nil {
		return
	}
	
	// Get agent identity from context (thread-safe for parallel execution)
	agentID := getAgentIdentity(ctx)

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
		Type:          session.EventToolResult,
		CorrelationID: corrID,
		Goal:          e.currentGoal,
		Tool:          name,
		Args:          args,
		Content:       content,
		DurationMs:    duration.Milliseconds(),
		Agent:         agentID.Name,
		AgentRole:     agentID.Role,
		Timestamp:     time.Now(),
	}
	if err != nil {
		event.Error = err.Error()
	}
	e.session.Events = append(e.session.Events, event)
	e.sessionManager.Update(e.session)
}

// logLLMCall logs full LLM request/response details for forensic replay.
// This captures everything needed for -vv verbosity in replay.
func (e *Executor) logLLMCall(ctx context.Context, eventType string, messages []llm.Message, resp *llm.ChatResponse, duration time.Duration) {
	if e.session == nil || e.sessionManager == nil {
		return
	}

	agentID := getAgentIdentity(ctx)

	// Build full prompt from messages
	var promptBuilder strings.Builder
	for _, msg := range messages {
		promptBuilder.WriteString(fmt.Sprintf("[%s]\n%s\n\n", msg.Role, msg.Content))
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Args)
				promptBuilder.WriteString(fmt.Sprintf("  tool_call: %s(%s)\n", tc.Name, string(argsJSON)))
			}
		}
		if msg.ToolCallID != "" {
			promptBuilder.WriteString(fmt.Sprintf("  tool_result_for: %s\n", msg.ToolCallID))
		}
	}

	event := session.Event{
		Type:      eventType,
		Goal:      e.currentGoal,
		Content:   resp.Content,
		Agent:     agentID.Name,
		AgentRole: agentID.Role,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			Model:     resp.Model,
			LatencyMs: duration.Milliseconds(),
			TokensIn:  resp.InputTokens,
			TokensOut: resp.OutputTokens,
			Prompt:    promptBuilder.String(),
			Response:  resp.Content,
			Thinking:  resp.Thinking,
		},
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
	e.logPhaseSuperviseWithDetails(goal, step, verdict, guidance, humanRequired, durationMs, "execution", "", "", "", "")
}

// logPhaseSuperviseWithDetails logs the SUPERVISE phase with full LLM details.
func (e *Executor) logPhaseSuperviseWithDetails(goal, step, verdict, guidance string, humanRequired bool, durationMs int64, supervisorType, model, prompt, response, thinking string) {
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
			Phase:          "SUPERVISE",
			SupervisorType: supervisorType,
			Verdict:        verdict,
			Guidance:       guidance,
			HumanRequired:  humanRequired,
			Result:         verdict,
			Model:          model,
			Prompt:         prompt,
			Response:       response,
			Thinking:       thinking,
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
	e.logSecurityBlockWithTaint(blockID, trust, blockType, source, xmlBlock, entropy, nil)
}

// logSecurityBlockWithTaint logs untrusted content registration with taint lineage.
func (e *Executor) logSecurityBlockWithTaint(blockID, trust, blockType, source, xmlBlock string, entropy float64, taintedBy []string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventSecurityBlock,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			BlockID:       blockID,
			Trust:         trust,
			BlockType:     blockType,
			Source:        source,
			Entropy:       entropy,
			XMLBlock:      xmlBlock,
			RelatedBlocks: taintedBy, // Blocks that influenced this block's creation
		},
	})
	e.sessionManager.Update(e.session)
}

// logSecurityStatic logs static/deterministic check to session.
func (e *Executor) logSecurityStatic(tool, blockID string, relatedBlockIDs []string, pass bool, flags []string, skipReason string, taintLineage []*security.TaintLineageNode) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventSecurityStatic,
		Tool:      tool,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			CheckName:     "static",
			BlockID:       blockID,
			RelatedBlocks: relatedBlockIDs,
			Pass:          pass,
			Flags:         flags,
			SkipReason:    skipReason,
			TaintLineage:  convertTaintLineage(taintLineage),
		},
	})
	e.sessionManager.Update(e.session)
}

// convertTaintLineage converts security package lineage to session package format.
func convertTaintLineage(nodes []*security.TaintLineageNode) []session.TaintNode {
	if len(nodes) == 0 {
		return nil
	}
	result := make([]session.TaintNode, 0, len(nodes))
	for _, n := range nodes {
		result = append(result, convertTaintNode(n))
	}
	return result
}

func convertTaintNode(n *security.TaintLineageNode) session.TaintNode {
	if n == nil {
		return session.TaintNode{}
	}
	node := session.TaintNode{
		BlockID:  n.BlockID,
		Trust:    string(n.Trust),
		Source:   n.Source,
		EventSeq: n.EventSeq,
		Depth:    n.Depth,
	}
	for _, child := range n.TaintedBy {
		node.TaintedBy = append(node.TaintedBy, convertTaintNode(child))
	}
	return node
}

// logSecurityTriage logs LLM triage check to session.
func (e *Executor) logSecurityTriage(tool, blockID string, suspicious bool, model string, latencyMs int64, skipReason string) {
	e.logSecurityTriageWithDetails(tool, blockID, suspicious, model, latencyMs, "", "", "", skipReason)
}

// logSecurityTriageWithDetails logs LLM triage with full details.
func (e *Executor) logSecurityTriageWithDetails(tool, blockID string, suspicious bool, model string, latencyMs int64, prompt, response, thinking, skipReason string) {
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
			Prompt:     prompt,
			Response:   response,
			Thinking:   thinking,
			SkipReason: skipReason,
		},
	})
	e.sessionManager.Update(e.session)
}

// logSecuritySupervisor logs supervisor review to session.
func (e *Executor) logSecuritySupervisor(tool, blockID, verdict, reason, model string, latencyMs int64) {
	e.logSecuritySupervisorWithDetails(tool, blockID, verdict, reason, model, latencyMs, "", "", "")
}

// logSecuritySupervisorWithDetails logs supervisor review with full LLM details.
func (e *Executor) logSecuritySupervisorWithDetails(tool, blockID, verdict, reason, model string, latencyMs int64, prompt, response, thinking string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventSecuritySupervisor,
		Tool:       tool,
		DurationMs: latencyMs,
		Timestamp:  time.Now(),
		Meta: &session.EventMeta{
			SupervisorType: "security",
			CheckName:      "supervisor",
			BlockID:        blockID,
			Verdict:        verdict,
			Reason:         reason,
			Model:          model,
			LatencyMs:      latencyMs,
			Prompt:         prompt,
			Response:       response,
			Thinking:       thinking,
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

// logSubAgentStart logs when a sub-agent is spawned.
func (e *Executor) logSubAgentStart(name, role, model, task string, inputs map[string]string) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:      session.EventSubAgentStart,
		Goal:      e.currentGoal,
		Timestamp: time.Now(),
		Meta: &session.EventMeta{
			SubAgentName:   name,
			SubAgentRole:   role,
			SubAgentModel:  model,
			SubAgentTask:   task,
			SubAgentInputs: inputs,
		},
	})
	e.sessionManager.Update(e.session)
}

// logSubAgentEnd logs when a sub-agent completes with its output.
func (e *Executor) logSubAgentEnd(name, role, model, output string, durationMs int64, err error) {
	if e.session == nil || e.sessionManager == nil {
		return
	}
	errStr := ""
	success := true
	if err != nil {
		errStr = err.Error()
		success = false
	}
	e.session.Events = append(e.session.Events, session.Event{
		Type:       session.EventSubAgentEnd,
		Goal:       e.currentGoal,
		Timestamp:  time.Now(),
		DurationMs: durationMs,
		Success:    &success,
		Error:      errStr,
		Meta: &session.EventMeta{
			SubAgentName:   name,
			SubAgentRole:   role,
			SubAgentModel:  model,
			SubAgentOutput: output,
		},
	})
	e.sessionManager.Update(e.session)
}

// buildPriorGoalsContext builds a list of GoalOutput from completed goals.
// This provides context for sub-agents about what has already been accomplished.
func (e *Executor) buildPriorGoalsContext() []GoalOutput {
	var priorGoals []GoalOutput
	for goalName, output := range e.outputs {
		priorGoals = append(priorGoals, GoalOutput{
			ID:     goalName,
			Output: output,
		})
	}
	return priorGoals
}

// spawnDynamicAgent spawns a sub-agent with the given role and task.
// Sub-agents go through the same four-phase execution as main goals when supervision is enabled.
func (e *Executor) spawnDynamicAgent(ctx context.Context, role, task string, outputs []string) (string, error) {
	// Set sub-agent context in context.Context (thread-safe for parallel execution)
	ctx = withAgentIdentity(ctx, role, role)

	// Build system prompt for the sub-agent
	systemPrompt := fmt.Sprintf("You are a %s. Complete the following task thoroughly and return your findings.", role)

	// Inject security research framing if enabled
	if prefix := e.securityResearchPrefix(); prefix != "" {
		systemPrompt = prefix + systemPrompt
	}

	// Build XML task prompt for sub-agent
	taskDescription := task
	if len(outputs) > 0 {
		taskDescription += "\n\n" + buildStructuredOutputInstruction(outputs)
	}
	userPrompt := BuildTaskContext(role, e.currentGoal, taskDescription)

	// Log the spawn
	if e.OnSubAgentStart != nil {
		e.OnSubAgentStart(role, map[string]string{"task": task})
	}

	// Sub-agents inherit supervision from their parent goal
	// Only supervise if: parent goal is supervised AND supervision infrastructure is available
	supervised := e.currentGoalSupervised && e.supervisor != nil && e.checkpointStore != nil

	// ============================================
	// PHASE 1: COMMIT - Sub-agent declares intent
	// ============================================
	var preCheckpoint *checkpoint.PreCheckpoint
	if supervised {
		preCheckpoint = e.subAgentCommitPhase(ctx, role, task)
		if err := e.checkpointStore.SavePre(preCheckpoint); err != nil {
			e.logger.Warn("failed to save sub-agent pre-checkpoint", map[string]interface{}{
				"role":  role,
				"error": err.Error(),
			})
		} else {
			e.logCheckpoint("pre", role, "", preCheckpoint.StepID)
		}
		if e.OnSupervisionEvent != nil {
			e.OnSupervisionEvent(role, "commit", preCheckpoint)
		}
	}

	// ============================================
	// PHASE 2: EXECUTE - Sub-agent does the work
	// ============================================
	output, toolsUsed, err := e.subAgentExecutePhase(ctx, role, systemPrompt, userPrompt)
	if err != nil {
		return "", err
	}

	// ============================================
	// PHASE 3 & 4: RECONCILE & SUPERVISE
	// ============================================
	if supervised && preCheckpoint != nil {
		// Create post-checkpoint with self-assessment
		postCheckpoint := e.subAgentPostCheckpoint(ctx, role, preCheckpoint, output, toolsUsed)
		if err := e.checkpointStore.SavePost(postCheckpoint); err != nil {
			e.logger.Warn("failed to save sub-agent post-checkpoint", map[string]interface{}{
				"role":  role,
				"error": err.Error(),
			})
		} else {
			e.logCheckpoint("post", role, "", postCheckpoint.StepID)
		}
		if e.OnSupervisionEvent != nil {
			e.OnSupervisionEvent(role, "execute", postCheckpoint)
		}

		// RECONCILE: Static pattern checks
		reconcileStart := time.Now()
		reconcileResult := e.supervisor.Reconcile(preCheckpoint, postCheckpoint)
		reconcileDuration := time.Since(reconcileStart).Milliseconds()

		if err := e.checkpointStore.SaveReconcile(reconcileResult); err != nil {
			e.logger.Warn("failed to save sub-agent reconcile result", map[string]interface{}{
				"role":  role,
				"error": err.Error(),
			})
		}
		e.logPhaseReconcile(role, reconcileResult.StepID, reconcileResult.Triggers, reconcileResult.Supervise, reconcileDuration)

		if e.OnSupervisionEvent != nil {
			e.OnSupervisionEvent(role, "reconcile", reconcileResult)
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
				false, // Sub-agents don't require human approval
			)
			superviseDuration := time.Since(superviseStart).Milliseconds()

			if err != nil {
				return "", fmt.Errorf("sub-agent supervision failed: %w", err)
			}

			if err := e.checkpointStore.SaveSupervise(superviseResult); err != nil {
				e.logger.Warn("failed to save sub-agent supervise result", map[string]interface{}{
					"role":  role,
					"error": err.Error(),
				})
			}
			e.logPhaseSupervise(role, superviseResult.StepID, superviseResult.Verdict, superviseResult.Correction, false, superviseDuration)

			if e.OnSupervisionEvent != nil {
				e.OnSupervisionEvent(role, "supervise", superviseResult)
			}

			// Handle verdict
			switch supervision.Verdict(superviseResult.Verdict) {
			case supervision.VerdictReorient:
				// Re-execute with correction in XML format
				e.logger.Info("reorienting sub-agent execution", map[string]interface{}{
					"role":       role,
					"correction": superviseResult.Correction,
				})
				correctedTask := BuildTaskContextWithCorrection(role, e.currentGoal, taskDescription, superviseResult.Correction)
				output, _, err = e.subAgentExecutePhase(ctx, role, systemPrompt, correctedTask)
				if err != nil {
					return "", err
				}

			case supervision.VerdictPause:
				// Sub-agents don't support human intervention - fail gracefully
				return "", fmt.Errorf("sub-agent %s paused by supervisor: %s", role, superviseResult.Question)
			}
		}
	}

	if e.OnSubAgentComplete != nil {
		e.OnSubAgentComplete(role, output)
	}
	e.extractAndStoreObservations(ctx, role, "AGENT", output)
	return output, nil
}

// spawnAgentWithPrompt spawns a sub-agent with a custom system prompt and optional profile.
// This is the unified entry point used by both AGENT entries and dynamic sub-agents.
// The profile parameter allows using a different LLM provider (e.g., "fast", "reasoning-heavy").
func (e *Executor) spawnAgentWithPrompt(ctx context.Context, role, systemPrompt, task string, outputs []string, profile string, priorGoals []GoalOutput) (string, error) {
	// Set sub-agent context
	ctx = withAgentIdentity(ctx, role, role)

	// Get the provider (use profile if specified, otherwise default)
	provider := e.provider
	if profile != "" {
		var err error
		provider, err = e.providerFactory.GetProvider(profile)
		if err != nil {
			return "", fmt.Errorf("failed to get provider for profile %q: %w", profile, err)
		}
	}

	// Inject tool guidance for sub-agents (they inherit parent's tools including memory)
	if e.registry != nil && e.registry.Has("memory_recall") {
		systemPrompt = SemanticMemoryGuidancePrefix + systemPrompt
	}
	if e.registry != nil && e.registry.Has("scratchpad_write") {
		systemPrompt = ScratchpadGuidancePrefix + systemPrompt
	}

	// Inject security research framing if enabled
	if prefix := e.securityResearchPrefix(); prefix != "" {
		systemPrompt = prefix + systemPrompt
	}

	// Build XML task prompt with or without prior goal context
	taskDescription := task
	if len(outputs) > 0 {
		taskDescription += "\n\n" + buildStructuredOutputInstruction(outputs)
	}
	var userPrompt string
	if len(priorGoals) > 0 {
		userPrompt = BuildTaskContextWithPriorGoals(role, e.currentGoal, taskDescription, priorGoals)
	} else {
		userPrompt = BuildTaskContext(role, e.currentGoal, taskDescription)
	}

	// Log the spawn
	inputs := make(map[string]string)
	for k, v := range e.inputs {
		inputs[k] = v
	}
	for k, v := range e.outputs {
		inputs[k] = v
	}
	e.logSubAgentStart(role, role, profile, task, inputs)

	if e.OnSubAgentStart != nil {
		e.OnSubAgentStart(role, map[string]string{"task": task})
	}

	// Sub-agents inherit supervision from their parent goal
	supervised := e.currentGoalSupervised && e.supervisor != nil && e.checkpointStore != nil

	// PHASE 1: COMMIT
	var preCheckpoint *checkpoint.PreCheckpoint
	if supervised {
		preCheckpoint = e.subAgentCommitPhase(ctx, role, task)
		if err := e.checkpointStore.SavePre(preCheckpoint); err != nil {
			e.logger.Warn("failed to save sub-agent pre-checkpoint", map[string]interface{}{
				"role":  role,
				"error": err.Error(),
			})
		} else {
			e.logCheckpoint("pre", role, "", preCheckpoint.StepID)
		}
		if e.OnSupervisionEvent != nil {
			e.OnSupervisionEvent(role, "commit", preCheckpoint)
		}
	}

	// PHASE 2: EXECUTE (using the specified provider)
	output, toolsUsed, err := e.subAgentExecutePhaseWithProvider(ctx, provider, role, systemPrompt, userPrompt)
	if err != nil {
		return "", err
	}

	// PHASE 3 & 4: RECONCILE & SUPERVISE (same as spawnDynamicAgent)
	if supervised && preCheckpoint != nil {
		postCheckpoint := e.subAgentPostCheckpoint(ctx, role, preCheckpoint, output, toolsUsed)
		if err := e.checkpointStore.SavePost(postCheckpoint); err != nil {
			e.logger.Warn("failed to save sub-agent post-checkpoint", map[string]interface{}{
				"role":  role,
				"error": err.Error(),
			})
		} else {
			e.logCheckpoint("post", role, "", postCheckpoint.StepID)
		}
		if e.OnSupervisionEvent != nil {
			e.OnSupervisionEvent(role, "execute", postCheckpoint)
		}

		reconcileStart := time.Now()
		reconcileResult := e.supervisor.Reconcile(preCheckpoint, postCheckpoint)
		reconcileDuration := time.Since(reconcileStart).Milliseconds()

		if err := e.checkpointStore.SaveReconcile(reconcileResult); err != nil {
			e.logger.Warn("failed to save sub-agent reconcile result", map[string]interface{}{
				"role":  role,
				"error": err.Error(),
			})
		}
		e.logPhaseReconcile(role, reconcileResult.StepID, reconcileResult.Triggers, reconcileResult.Supervise, reconcileDuration)

		if e.OnSupervisionEvent != nil {
			e.OnSupervisionEvent(role, "reconcile", reconcileResult)
		}

		if reconcileResult.Supervise {
			superviseStart := time.Now()
			decisionTrail := e.checkpointStore.GetDecisionTrail()
			superviseResult, err := e.supervisor.Supervise(
				ctx,
				preCheckpoint,
				postCheckpoint,
				reconcileResult.Triggers,
				decisionTrail,
				false,
			)
			superviseDuration := time.Since(superviseStart).Milliseconds()

			if err != nil {
				return "", fmt.Errorf("sub-agent supervision failed: %w", err)
			}

			if err := e.checkpointStore.SaveSupervise(superviseResult); err != nil {
				e.logger.Warn("failed to save sub-agent supervise result", map[string]interface{}{
					"role":  role,
					"error": err.Error(),
				})
			}
			e.logPhaseSupervise(role, superviseResult.StepID, superviseResult.Verdict, superviseResult.Correction, false, superviseDuration)

			if e.OnSupervisionEvent != nil {
				e.OnSupervisionEvent(role, "supervise", superviseResult)
			}

			switch supervision.Verdict(superviseResult.Verdict) {
			case supervision.VerdictReorient:
				correctedTask := BuildTaskContextWithCorrection(role, e.currentGoal, taskDescription, superviseResult.Correction)
				output, _, err = e.subAgentExecutePhaseWithProvider(ctx, provider, role, systemPrompt, correctedTask)
				if err != nil {
					return "", err
				}
			case supervision.VerdictPause:
				return "", fmt.Errorf("sub-agent %s paused by supervisor: %s", role, superviseResult.Question)
			}
		}
	}

	if e.OnSubAgentComplete != nil {
		e.OnSubAgentComplete(role, output)
	}
	e.extractAndStoreObservations(ctx, role, "AGENT", output)
	return output, nil
}

// subAgentExecutePhaseWithProvider runs the sub-agent execution loop with a specific provider.
func (e *Executor) subAgentExecutePhaseWithProvider(ctx context.Context, provider llm.Provider, role, systemPrompt, userPrompt string) (output string, toolsUsed []string, err error) {
	start := time.Now()
	stepID := fmt.Sprintf("subagent:%s", role)
	e.logger.PhaseStart("EXECUTE", role, stepID)

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	// Build tool definitions (excluding spawn_agent and spawn_agents to enforce depth=1)
	var toolDefs []llm.ToolDef
	if e.registry != nil {
		for _, def := range e.registry.Definitions() {
			if def.Name != "spawn_agent" && def.Name != "spawn_agents" {
				toolDefs = append(toolDefs, llm.ToolDef{
					Name:        def.Name,
					Description: def.Description,
					Parameters:  def.Parameters,
				})
			}
		}
	}

	// Add MCP tools
	if e.mcpManager != nil {
		for _, t := range e.mcpManager.AllTools() {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        fmt.Sprintf("mcp_%s_%s", t.Server, t.Tool.Name),
				Description: fmt.Sprintf("[MCP:%s] %s", t.Server, t.Tool.Description),
				Parameters:  t.Tool.InputSchema,
			})
		}
	}

	toolsUsedMap := make(map[string]bool)

	// Execute sub-agent loop
	for {
		llmStart := time.Now()
		resp, err := provider.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		llmDuration := time.Since(llmStart)
		if err != nil {
			e.logger.PhaseComplete("EXECUTE", role, stepID, time.Since(start), "error")
			return "", nil, fmt.Errorf("sub-agent LLM error: %w", err)
		}

		// Log full LLM interaction (for -vv replay)
		e.logLLMCall(ctx, session.EventAssistant, messages, resp, llmDuration)

		// No tool calls = sub-agent complete
		if len(resp.ToolCalls) == 0 {
			for tool := range toolsUsedMap {
				toolsUsed = append(toolsUsed, tool)
			}
			e.logger.PhaseComplete("EXECUTE", role, stepID, time.Since(start), "complete")
			return resp.Content, toolsUsed, nil
		}

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

// subAgentCommitPhase asks the sub-agent to declare its intent before execution.
func (e *Executor) subAgentCommitPhase(ctx context.Context, role, task string) *checkpoint.PreCheckpoint {
	start := time.Now()
	stepID := fmt.Sprintf("subagent:%s", role)
	e.logger.PhaseStart("COMMIT", role, stepID)

	commitPrompt := fmt.Sprintf(`Before executing this task, declare your intent:

ROLE: %s
TASK: %s

Respond with a JSON object:
{
  "interpretation": "How you understand this task",
  "scope_in": ["What you will do"],
  "scope_out": ["What you will NOT do"],
  "approach": "Your planned approach",
  "tools_planned": ["tools you expect to use"],
  "predicted_output": "What you expect to produce",
  "confidence": "high|medium|low",
  "assumptions": ["Assumptions you are making"]
}`, role, task)

	messages := []llm.Message{
		{Role: "system", Content: "You are declaring your intent before executing a task. Be specific and honest about your plans."},
		{Role: "user", Content: commitPrompt},
	}

	resp, err := e.provider.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})

	pre := &checkpoint.PreCheckpoint{
		StepID:      stepID,
		StepType:    "SUBAGENT",
		Instruction: task,
		Timestamp:   time.Now(),
		Metadata:    map[string]string{"role": role},
	}

	if err != nil {
		e.logger.Warn("sub-agent commit phase LLM error", map[string]interface{}{"role": role, "error": err.Error()})
		pre.Confidence = "low"
		pre.Assumptions = []string{"Failed to get commitment from sub-agent"}
		e.logger.PhaseComplete("COMMIT", role, stepID, time.Since(start), "error")
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

	if pre.Confidence == "" {
		pre.Confidence = "medium"
	}

	durationMs := time.Since(start).Milliseconds()
	e.logPhaseCommit(role, pre.Interpretation, pre.Confidence, durationMs)
	e.logger.PhaseComplete("COMMIT", role, stepID, time.Since(start), "ok")

	return pre
}

// subAgentExecutePhase runs the sub-agent execution loop.
func (e *Executor) subAgentExecutePhase(ctx context.Context, role, systemPrompt, userPrompt string) (output string, toolsUsed []string, err error) {
	start := time.Now()
	stepID := fmt.Sprintf("subagent:%s", role)
	e.logger.PhaseStart("EXECUTE", role, stepID)

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	// Build tool definitions (excluding spawn_agent and spawn_agents to enforce depth=1)
	var toolDefs []llm.ToolDef
	for _, def := range e.registry.Definitions() {
		if def.Name != "spawn_agent" && def.Name != "spawn_agents" {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			})
		}
	}

	// Add MCP tools
	if e.mcpManager != nil {
		for _, t := range e.mcpManager.AllTools() {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        fmt.Sprintf("mcp_%s_%s", t.Server, t.Tool.Name),
				Description: fmt.Sprintf("[MCP:%s] %s", t.Server, t.Tool.Description),
				Parameters:  t.Tool.InputSchema,
			})
		}
	}

	toolsUsedMap := make(map[string]bool)

	// Execute sub-agent loop
	for {
		resp, err := e.provider.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			e.logger.PhaseComplete("EXECUTE", role, stepID, time.Since(start), "error")
			return "", nil, fmt.Errorf("sub-agent LLM error: %w", err)
		}

		// No tool calls = sub-agent complete
		if len(resp.ToolCalls) == 0 {
			for tool := range toolsUsedMap {
				toolsUsed = append(toolsUsed, tool)
			}
			e.logger.PhaseComplete("EXECUTE", role, stepID, time.Since(start), "complete")
			return resp.Content, toolsUsed, nil
		}

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

		// Execute tool calls in parallel (security verification happens in executeTool)
		toolMessages := e.executeToolsParallel(ctx, resp.ToolCalls)
		messages = append(messages, toolMessages...)
	}
}

// subAgentPostCheckpoint creates a post-checkpoint with self-assessment for sub-agents.
func (e *Executor) subAgentPostCheckpoint(ctx context.Context, role string, pre *checkpoint.PreCheckpoint, output string, toolsUsed []string) *checkpoint.PostCheckpoint {
	assessPrompt := fmt.Sprintf(`You just completed a task. Assess your work:

ROLE: %s
ORIGINAL TASK: %s

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
}`, role, pre.Instruction, pre.Interpretation, pre.Approach, pre.PredictedOutput, output, strings.Join(toolsUsed, ", "))

	messages := []llm.Message{
		{Role: "system", Content: "You are honestly assessing whether your work met your commitment."},
		{Role: "user", Content: assessPrompt},
	}

	resp, err := e.provider.Chat(ctx, llm.ChatRequest{
		Messages: messages,
	})

	post := &checkpoint.PostCheckpoint{
		StepID:       pre.StepID,
		ActualOutput: output,
		ToolsUsed:    toolsUsed,
		Timestamp:    time.Now(),
	}

	if err != nil {
		e.logger.Warn("sub-agent post-checkpoint LLM error", map[string]interface{}{"role": role, "error": err.Error()})
		post.MetCommitment = false
		post.Concerns = []string{"Failed to get self-assessment from sub-agent"}
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
			post.MetCommitment = true
		}
	} else {
		post.MetCommitment = true
	}

	return post
}

// securityResearchPrefix returns the defensive framing prefix for security research mode.
func (e *Executor) securityResearchPrefix() string {
	if e.securityResearchScope == "" {
		return ""
	}
	return fmt.Sprintf(`SECURITY RESEARCH CONTEXT:
You are conducting authorized security research. Your work is legitimate and within scope.

Authorized Scope: %s

This research may involve exploring attack vectors, vulnerability analysis, penetration testing,
or other offensive security techniques. All activities are authorized for the purpose of
improving defensive capabilities and security posture.

When explaining security concepts:
- Focus on understanding threats to build better defenses
- Document attack vectors so they can be mitigated
- Analyze vulnerabilities to develop patches and controls
- Test exploitation to verify defensive measures work

`, e.securityResearchScope)
}

// OrchestratorSystemPromptPrefix returns the prefix to inject when spawn_agent is available.
const OrchestratorSystemPromptPrefix = `You can spawn sub-agents to delegate work when genuinely needed.

CORE PRINCIPLE: Effort should be proportional to the task. Simple tasks should be done directly.

DO spawn sub-agents when:
- Task genuinely benefits from parallel independent work (e.g., researching 5 different topics)
- Specialized roles add real value (e.g., researcher + critic for important decisions)
- The task is complex enough to justify the overhead

DO NOT spawn sub-agents when:
- You can handle it directly in a few steps
- The task is straightforward (simple lookups, single file edits, basic questions)
- Delegation would add overhead without meaningful benefit
- You're tempted to spawn just to "be thorough" — thoroughness doesn't require sub-agents

IMPORTANT: When you do spawn multiple agents, use unique descriptive role names (e.g., "market-researcher", "competitor-analyst" not multiple "researcher" agents).

`

// ScratchpadGuidancePrefix is injected when scratchpad tools are available.
const ScratchpadGuidancePrefix = `SCRATCHPAD: Use scratchpad extensively to accelerate future goals.

WRITE proactively when you discover:
- Facts, values, or config that may be reused (e.g., "api_endpoint", "user_timezone", "repo_branch")
- Computed results that were expensive to obtain
- Decisions or preferences expressed by the user

READ before recomputing:
- Check scratchpad first for information that might already exist

DISCOVER what's stored:
- Use scratchpad_list("") to see ALL keys
- Use scratchpad_list("api") to find keys containing "api" (substring match)
- Use scratchpad_search("term") for fuzzy search in BOTH keys AND values

When unsure what's available, list first: scratchpad_list("") shows everything.

KEY NAMING: Use descriptive, consistent keys with underscores (e.g., "project_deadline", "api_base_url").

`

// SemanticMemoryGuidancePrefix is injected when semantic memory tools are available.
const SemanticMemoryGuidancePrefix = `🧠 PERSISTENT KNOWLEDGE BASE — CHECK FIRST!

You have a PERSISTENT knowledge base that survives across sessions. This is NOT temporary scratch space.

MANDATORY FIRST STEP for any research/decision task:
→ memory_recall("relevant topic") BEFORE web search, file reading, or MCP calls

WHY: You may have already researched this. Don't waste time re-discovering what you learned before.

WHAT'S IN YOUR KNOWLEDGE BASE:
- Findings: facts discovered during past work
- Insights: conclusions, patterns, architectural decisions
- Lessons: what worked, what failed, mistakes to avoid

EXAMPLES:
- memory_recall("authentication") → finds past auth decisions
- memory_recall("user preferences") → finds what the user likes
- memory_recall("API rate limits") → finds past research on APIs

The search is SEMANTIC — it matches meaning, not just keywords.
"database choice" finds "We chose PostgreSQL for JSON support"

`

// Run executes the workflow.
func (e *Executor) Run(ctx context.Context, inputs map[string]string) (*Result, error) {
	startTime := time.Now()
	workflowName := e.workflow.Name
	if workflowName == "" {
		workflowName = "unnamed"
	}
	e.logger.ExecutionStart(workflowName)

	// Set main agent identity in context (workflow name as both name and role)
	ctx = withAgentIdentity(ctx, workflowName, "main")

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
func (e *Executor) executeTool(ctx context.Context, tc llm.ToolCallResponse) (interface{}, error) {
	start := time.Now()
	
	// Get agent identity early for error callbacks
	agentID := getAgentIdentity(ctx)

	// Security verification before execution
	if err := e.verifyToolCall(ctx, tc.Name, tc.Args); err != nil {
		e.logToolResult(ctx, tc.Name, tc.Args, "", nil, err, time.Since(start))
		if e.OnToolError != nil {
			e.OnToolError(tc.Name, tc.Args, err, agentID.Role)
		}
		return nil, err
	}

	// Log the tool call (returns correlation ID for linking to result)
	corrID := e.logToolCall(ctx, tc.Name, tc.Args)

	// Check if it's an MCP tool
	if strings.HasPrefix(tc.Name, "mcp_") {
		result, err := e.executeMCPTool(ctx, tc)
		duration := time.Since(start)
		e.logToolResult(ctx, tc.Name, tc.Args, corrID, result, err, duration)

		// MCP tools return external content - register as untrusted
		if err == nil && result != nil {
			e.registerUntrustedResult(ctx, tc.Name, result)
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
	e.logToolResult(ctx, tc.Name, tc.Args, corrID, result, err, duration)

	// Register external tool results as untrusted content
	if err == nil && result != nil && isExternalTool(tc.Name) {
		e.registerUntrustedResult(ctx, tc.Name, result)
	}

	if err != nil && e.OnToolError != nil {
		e.OnToolError(tc.Name, tc.Args, err, agentID.Role)
	}

	if e.OnToolCall != nil {
		e.OnToolCall(tc.Name, tc.Args, result, agentID.Role)
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
func (e *Executor) registerUntrustedResult(ctx context.Context, toolName string, result interface{}) {
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

	// Register as untrusted content block with taint from influencing blocks
	source := fmt.Sprintf("tool:%s", toolName)
	e.AddUntrustedContentWithTaint(ctx, content, source, e.lastSecurityRelatedBlocks)
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
