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

	"github.com/openclaw/headless-agent/internal/agentfile"
	"github.com/openclaw/headless-agent/internal/llm"
	"github.com/openclaw/headless-agent/internal/logging"
	"github.com/openclaw/headless-agent/internal/mcp"
	"github.com/openclaw/headless-agent/internal/policy"
	"github.com/openclaw/headless-agent/internal/session"
	"github.com/openclaw/headless-agent/internal/skills"
	"github.com/openclaw/headless-agent/internal/subagent"
	"github.com/openclaw/headless-agent/internal/tools"
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

// executeGoalWithTracking executes a single goal and tracks whether tool calls were made.
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

	// Build system message with skills context
	systemMsg := "You are a helpful assistant executing a workflow goal."
	
	// If spawn_agent tool is available, inject orchestrator guidance
	if e.registry.Has("spawn_agent") {
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

	// Track if any tool calls were made
	toolCallsMade := false

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
			return nil, fmt.Errorf("LLM error: %w", err)
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
			output := resp.Content
			
			// Parse structured output if declared
			if len(goal.Outputs) > 0 {
				parsedOutputs, err := parseStructuredOutput(resp.Content, goal.Outputs)
				if err != nil {
					// Log warning but don't fail - keep raw output
					e.logEvent(session.EventSystem, fmt.Sprintf("Warning: failed to parse structured output: %v", err))
				} else {
					// Store each output field as a variable
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

		toolCallsMade = true

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
	
	// Log the tool call
	e.logToolCall(tc.Name, tc.Args)
	
	// Check if it's an MCP tool
	if strings.HasPrefix(tc.Name, "mcp_") {
		result, err := e.executeMCPTool(ctx, tc)
		e.logToolResult(tc.Name, result, err, time.Since(start))
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

	if err != nil && e.OnToolError != nil {
		e.OnToolError(tc.Name, tc.Args, err)
	}

	if e.OnToolCall != nil {
		e.OnToolCall(tc.Name, tc.Args, result)
	}

	return result, err
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
