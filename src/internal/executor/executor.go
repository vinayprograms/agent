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
	e.registry.SetSpawner(func(ctx context.Context, role, task string) (string, error) {
		return e.spawnDynamicAgent(ctx, role, task)
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
func (e *Executor) spawnDynamicAgent(ctx context.Context, role, task string) (string, error) {
	// Build system prompt for the sub-agent
	systemPrompt := fmt.Sprintf("You are a %s. Complete the following task thoroughly and return your findings.\n\nTask: %s", role, task)

	// Log the spawn
	if e.OnSubAgentStart != nil {
		e.OnSubAgentStart(role, map[string]string{"task": task})
	}

	// Create messages for the sub-agent
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task},
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

		// Execute tool calls
		for _, tc := range resp.ToolCalls {
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
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
			})
		}
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
	// Bind inputs
	if err := e.bindInputs(inputs); err != nil {
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
				return result, err
			}
		} else if step.Type == agentfile.StepLOOP {
			if err := e.executeLoopStep(ctx, step, result); err != nil {
				result.Status = StatusFailed
				result.Error = err.Error()
				return result, err
			}
		}
	}

	result.Status = StatusComplete
	result.Outputs = e.outputs
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
			if e.OnGoalComplete != nil {
				e.OnGoalComplete(goal.Name, resp.Content)
			}
			e.logGoalEnd(goal.Name, resp.Content)
			return &GoalResult{Output: resp.Content, ToolCallsMade: toolCallsMade}, nil
		}

		toolCallsMade = true

		// Add assistant message with tool calls
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute tool calls
		for _, tc := range resp.ToolCalls {
			result, err := e.executeTool(ctx, tc)
			var resultStr string
			if err != nil {
				resultStr = fmt.Sprintf("Error: %v", err)
			} else {
				resultStr = fmt.Sprintf("%v", result)
			}

			messages = append(messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    resultStr,
			})
		}
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

	// Collect outputs
	var agentOutputs []string
	for _, result := range results {
		if result.Error != nil {
			return "", fmt.Errorf("sub-agent %s failed: %w", result.Name, result.Error)
		}
		agentOutputs = append(agentOutputs, fmt.Sprintf("[%s]: %s", result.Name, result.Output))
	}

	// If single agent, return directly
	if len(agentOutputs) == 1 {
		output := results[0].Output
		if e.OnGoalComplete != nil {
			e.OnGoalComplete(goal.Name, output)
		}
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
