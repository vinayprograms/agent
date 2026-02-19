// Sub-agent spawning and execution functions for the executor.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/supervision"
	"github.com/vinayprograms/agentkit/llm"
)

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
	systemPrompt := TersenessGuidance + fmt.Sprintf("You are a %s. Complete the task and return your findings.", role)

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
func (e *Executor) spawnAgentWithPrompt(ctx context.Context, role, systemPrompt, task string, outputs []string, profile string, priorGoals []GoalOutput, agentSupervised bool) (string, error) {
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
	if e.registry != nil && e.registry.Has("recall") {
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

	// Agent is supervised if: agent has SUPERVISED flag OR parent goal is supervised
	// Infrastructure must also be available (supervisor + checkpoint store)
	supervised := (agentSupervised || e.currentGoalSupervised) && e.supervisor != nil && e.checkpointStore != nil

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
const ScratchpadGuidancePrefix = `SCRATCHPAD (session-only working notes):

⚠️ TWO SEPARATE MEMORY SYSTEMS — DON'T MIX!
  • scratchpad_write → scratchpad_read (session only, exact key match)
  • remember → recall (persistent across runs, semantic search)
If you write to scratchpad, READ from scratchpad. If you remember(), recall().

WRITE proactively when you discover:
- Facts, values, or config that may be reused (e.g., "api_endpoint", "user_timezone")
- Computed results that were expensive to obtain
- Decisions or preferences expressed by the user

READ before recomputing:
- Check scratchpad first for information that might already exist

DISCOVER what's stored:
- scratchpad_list("") shows ALL keys
- scratchpad_list("api") finds keys containing "api"

KEY NAMING: Use descriptive keys with underscores (e.g., "project_deadline", "api_base_url").

`

// TersenessGuidance is prepended to execution prompts to reduce verbosity.
const TersenessGuidance = `OUTPUT RULES (HEADLESS AGENT — no human is reading this):
• Output ONLY the requested data/results
• NO preamble ("I'll help you...", "Let me...", "Now I can see...")
• NO narration ("First I'll...", "Next I need to...", "I have all the data...")
• NO filler ("Great question!", "Certainly!", "I understand...")
• NO sign-offs ("Let me know if...", "Hope this helps!")
• Think silently, output results only

`

// SemanticMemoryGuidancePrefix is injected when semantic memory tools are available.
const SemanticMemoryGuidancePrefix = `🧠 PERSISTENT KNOWLEDGE BASE (remember/recall):

⚠️ TWO SEPARATE MEMORY SYSTEMS — DON'T MIX!
  • remember → recall (persistent across runs, semantic search)
  • scratchpad_write → scratchpad_read (session only, exact key match)
If you remember(), use recall() to find it. NOT scratchpad_read.

MANDATORY FIRST STEP for research/decision tasks:
→ recall("relevant topic") BEFORE web search, file reading, or MCP calls

WHAT'S IN YOUR KNOWLEDGE BASE:
- Findings: facts discovered during past work
- Insights: conclusions, patterns, architectural decisions
- Lessons: what worked, what failed, mistakes to avoid

EXAMPLES:
- recall("authentication") → finds past auth decisions
- recall("API rate limits") → finds past research on APIs

The search uses KEYWORDS (BM25) — use distinctive terms, not sentences.

`

// Run executes the workflow.
