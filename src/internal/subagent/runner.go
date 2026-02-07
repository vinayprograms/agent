// Package subagent provides isolated sub-agent execution.
package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agent/internal/llm"
	"github.com/vinayprograms/agent/internal/mcp"
	"github.com/vinayprograms/agent/internal/packaging"
	"github.com/vinayprograms/agent/internal/policy"
	"github.com/vinayprograms/agent/internal/skills"
	"github.com/vinayprograms/agent/internal/tools"
)

// Runner spawns and manages sub-agents.
type Runner struct {
	// Factory for creating LLM providers per profile
	providerFactory llm.ProviderFactory

	// Base paths for resolving packages
	packagePaths []string

	// Callbacks
	OnSubAgentStart    func(name string, input map[string]string)
	OnSubAgentComplete func(name string, output string)
	OnSubAgentError    func(name string, err error)
}

// NewRunner creates a sub-agent runner.
func NewRunner(factory llm.ProviderFactory, packagePaths []string) *Runner {
	return &Runner{
		providerFactory: factory,
		packagePaths:    packagePaths,
	}
}

// SubAgentResult contains the result of a sub-agent execution.
type SubAgentResult struct {
	Name   string
	Output string
	Error  error
}

// SpawnOne spawns a single sub-agent and waits for completion.
func (r *Runner) SpawnOne(ctx context.Context, agent *agentfile.Agent, input map[string]string) (*SubAgentResult, error) {
	if r.OnSubAgentStart != nil {
		r.OnSubAgentStart(agent.Name, input)
	}

	result := &SubAgentResult{Name: agent.Name}

	// Load the agent package
	pkg, err := r.loadPackage(agent.FromPath)
	if err != nil {
		result.Error = fmt.Errorf("failed to load agent package: %w", err)
		if r.OnSubAgentError != nil {
			r.OnSubAgentError(agent.Name, result.Error)
		}
		return result, result.Error
	}

	// Create isolated execution environment
	env, err := r.createIsolatedEnv(pkg, agent.Requires)
	if err != nil {
		result.Error = fmt.Errorf("failed to create isolated env: %w", err)
		if r.OnSubAgentError != nil {
			r.OnSubAgentError(agent.Name, result.Error)
		}
		return result, result.Error
	}
	defer env.Cleanup()

	// Execute the sub-agent workflow
	output, err := env.Execute(ctx, input)
	if err != nil {
		result.Error = err
		if r.OnSubAgentError != nil {
			r.OnSubAgentError(agent.Name, err)
		}
		return result, err
	}

	result.Output = output
	if r.OnSubAgentComplete != nil {
		r.OnSubAgentComplete(agent.Name, output)
	}

	return result, nil
}

// SpawnParallel spawns multiple sub-agents in parallel.
func (r *Runner) SpawnParallel(ctx context.Context, agents []*agentfile.Agent, input map[string]string) ([]*SubAgentResult, error) {
	results := make([]*SubAgentResult, len(agents))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstError error

	for i, agent := range agents {
		wg.Add(1)
		go func(idx int, ag *agentfile.Agent) {
			defer wg.Done()

			result, err := r.SpawnOne(ctx, ag, input)

			mu.Lock()
			results[idx] = result
			if err != nil && firstError == nil {
				firstError = err
			}
			mu.Unlock()
		}(i, agent)
	}

	wg.Wait()
	return results, firstError
}

// loadPackage loads an agent package from the configured paths.
func (r *Runner) loadPackage(name string) (*packaging.Package, error) {
	// Try each package path
	for _, basePath := range r.packagePaths {
		pkg, err := packaging.LoadByName(basePath, name)
		if err == nil {
			return pkg, nil
		}
	}

	// Try as absolute/relative path
	if _, err := os.Stat(name); err == nil {
		return packaging.LoadFromPath(name)
	}
	if _, err := os.Stat(name + ".agent"); err == nil {
		return packaging.LoadFromPath(name + ".agent")
	}

	return nil, fmt.Errorf("agent package not found: %s", name)
}

// IsolatedEnv is an isolated execution environment for a sub-agent.
type IsolatedEnv struct {
	workflow *agentfile.Workflow
	provider llm.Provider
	registry *tools.Registry
	policy   *policy.Policy
	mcp      *mcp.Manager
	skills   []skills.SkillRef

	// Temp directory for extracted package
	tempDir string

	// Cleanup resources
	cleanupFuncs []func()
}

// createIsolatedEnv creates a fully isolated environment for sub-agent execution.
func (r *Runner) createIsolatedEnv(pkg *packaging.Package, profile string) (*IsolatedEnv, error) {
	env := &IsolatedEnv{}

	// Extract package to temp directory
	tempDir, err := pkg.ExtractToTemp()
	if err != nil {
		return nil, fmt.Errorf("failed to extract package: %w", err)
	}
	env.tempDir = tempDir
	env.cleanupFuncs = append(env.cleanupFuncs, func() {
		os.RemoveAll(tempDir)
	})

	// Load workflow from extracted Agentfile
	agentfilePath := filepath.Join(tempDir, "Agentfile")
	wf, err := agentfile.LoadFile(agentfilePath)
	if err != nil {
		env.Cleanup()
		return nil, fmt.Errorf("failed to load workflow: %w", err)
	}
	env.workflow = wf

	// Get provider for the profile
	provider, err := r.providerFactory.GetProvider(profile)
	if err != nil {
		env.Cleanup()
		return nil, fmt.Errorf("failed to get provider for profile %q: %w", profile, err)
	}
	env.provider = provider

	// Load policy from package
	policyPath := filepath.Join(tempDir, "policy.toml")
	if _, err := os.Stat(policyPath); err == nil {
		pol, err := policy.LoadFile(policyPath)
		if err != nil {
			env.Cleanup()
			return nil, fmt.Errorf("failed to load policy: %w", err)
		}
		env.policy = pol
	} else {
		// Use restrictive default policy
		env.policy = policy.NewRestrictive()
	}

	// Create isolated tool registry (only built-in tools, filtered by policy)
	env.registry = tools.NewRegistry(env.policy)

	// Load config and start MCP servers (isolated)
	configPath := filepath.Join(tempDir, "agent.json")
	if _, err := os.Stat(configPath); err == nil {
		cfg, err := config.LoadFile(configPath)
		if err == nil && len(cfg.MCP.Servers) > 0 {
			env.mcp = mcp.NewManager()
			for name, server := range cfg.MCP.Servers {
				// Convert config.MCPServerConfig to mcp.ServerConfig
				mcpConfig := mcp.ServerConfig{
					Command: server.Command,
					Args:    server.Args,
					Env:     server.Env,
				}
				if err := env.mcp.Connect(context.Background(), name, mcpConfig); err != nil {
					// Log but don't fail - server may not be available
					continue
				}
			}
			env.cleanupFuncs = append(env.cleanupFuncs, func() {
				env.mcp.Close()
			})
		}

		// Load skills from config paths
		if len(cfg.Skills.Paths) > 0 {
			for _, skillPath := range cfg.Skills.Paths {
				// Resolve relative to temp dir
				if !filepath.IsAbs(skillPath) {
					skillPath = filepath.Join(tempDir, skillPath)
				}
				refs, err := skills.Discover(skillPath)
				if err == nil {
					env.skills = append(env.skills, refs...)
				}
			}
		}
	}

	return env, nil
}

// Execute runs the workflow in this isolated environment.
func (env *IsolatedEnv) Execute(ctx context.Context, input map[string]string) (string, error) {
	// Build system message
	systemMsg := "You are executing an isolated sub-agent task. Complete the task and return your result."
	if len(env.skills) > 0 {
		systemMsg += "\n\nAvailable skills:\n"
		for _, ref := range env.skills {
			systemMsg += fmt.Sprintf("- %s: %s\n", ref.Name, ref.Description)
		}
	}

	// Get the first goal's outcome as the task
	if len(env.workflow.Goals) == 0 {
		return "", fmt.Errorf("workflow has no goals")
	}

	// Interpolate input into goal outcome
	task := env.workflow.Goals[0].Outcome
	for k, v := range input {
		task = replaceVar(task, k, v)
	}

	// Build messages
	messages := []llm.Message{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: task},
	}

	// Get tool definitions
	var toolDefs []llm.ToolDef
	if env.registry != nil {
		for _, def := range env.registry.Definitions() {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			})
		}
	}
	if env.mcp != nil {
		for _, t := range env.mcp.AllTools() {
			toolDefs = append(toolDefs, llm.ToolDef{
				Name:        fmt.Sprintf("mcp_%s_%s", t.Server, t.Tool.Name),
				Description: fmt.Sprintf("[MCP:%s] %s", t.Server, t.Tool.Description),
				Parameters:  t.Tool.InputSchema,
			})
		}
	}

	// Execute goal loop
	for {
		resp, err := env.provider.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			return "", fmt.Errorf("LLM error: %w", err)
		}

		// No tool calls = complete
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		// Add assistant message
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute tools
		for _, tc := range resp.ToolCalls {
			result, err := env.executeTool(ctx, tc)
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

// executeTool executes a tool call in the isolated environment.
func (env *IsolatedEnv) executeTool(ctx context.Context, tc llm.ToolCallResponse) (interface{}, error) {
	// Check for MCP tool
	if len(tc.Name) > 4 && tc.Name[:4] == "mcp_" && env.mcp != nil {
		parts := splitMCPToolName(tc.Name)
		if len(parts) == 2 {
			result, err := env.mcp.CallTool(ctx, parts[0], parts[1], tc.Args)
			if err != nil {
				return nil, err
			}
			var output string
			for _, c := range result.Content {
				if c.Type == "text" {
					output += c.Text
				}
			}
			return output, nil
		}
	}

	// Built-in tool
	if env.registry == nil {
		return nil, fmt.Errorf("no tool registry")
	}
	tool := env.registry.Get(tc.Name)
	if tool == nil {
		return nil, fmt.Errorf("tool not found: %s", tc.Name)
	}
	return tool.Execute(ctx, tc.Args)
}

// Cleanup releases resources.
func (env *IsolatedEnv) Cleanup() {
	for _, fn := range env.cleanupFuncs {
		fn()
	}
}

// Helper to replace $var in string
func replaceVar(s, name, value string) string {
	result := s
	placeholder := "$" + name
	for {
		idx := indexOf(result, placeholder)
		if idx < 0 {
			return result
		}
		result = result[:idx] + value + result[idx+len(placeholder):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func splitMCPToolName(name string) []string {
	// mcp_<server>_<tool> -> [server, tool]
	rest := name[4:] // strip "mcp_"
	for i := 0; i < len(rest); i++ {
		if rest[i] == '_' {
			return []string{rest[:i], rest[i+1:]}
		}
	}
	return nil
}
