package executor

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/vinayprograms/agent/internal/hooks"
	"github.com/vinayprograms/agentkit/llm"
	"golang.org/x/sync/errgroup"
)

// concurrencyLimit caps concurrent tool executions.
var concurrencyLimit = toolConcurrency(runtime.NumCPU())

// toolConcurrency oversubscribes CPUs 4x — tool work is I/O-bound (network,
// disk) — within bounds that keep a small machine responsive and a large one
// from overwhelming the services it calls.
func toolConcurrency(cpus int) int {
	return min(max(cpus*4, 4), 32)
}

// applyToolTimeout wraps the context with a timeout for network-dependent tools.
// Returns the original context if no timeout is configured for the tool.
func (e *Executor) applyToolTimeout(ctx context.Context, toolName string) (context.Context, context.CancelFunc) {
	var timeoutSec int

	switch {
	case strings.HasPrefix(toolName, "mcp_"):
		timeoutSec = e.timeoutMCP
	case toolName == "web_search":
		timeoutSec = e.timeoutWebSearch
	case toolName == "web_fetch":
		timeoutSec = e.timeoutWebFetch
	default:
		return ctx, nil // No timeout for other tools
	}

	if timeoutSec <= 0 {
		return ctx, nil // No timeout configured
	}

	// Only add timeout if context doesn't already have a shorter deadline
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) < time.Duration(timeoutSec)*time.Second {
			return ctx, nil // Existing deadline is shorter
		}
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	return ctx, cancel
}

func (e *Executor) executeTool(ctx context.Context, tc llm.ToolCallResponse) (string, error) {
	start := time.Now()

	// Get agent identity early for error callbacks
	agentID := getAgentIdentity(ctx)

	// Apply timeout based on tool type
	ctx, cancel := e.applyToolTimeout(ctx, tc.Name)
	if cancel != nil {
		defer cancel()
	}

	// Security verification before execution
	relatedBlocks, err := e.verifyToolCall(ctx, tc.Name, tc.Args)
	if err != nil {
		e.logToolResult(ctx, tc.Name, tc.Args, "", "", err, time.Since(start))
		e.hooks.Fire(ctx, hooks.ToolError, map[string]any{"name": tc.Name, "args": tc.Args, "error": err, "agent_role": agentID.Role})
		return "", err
	}

	// Log the tool call (returns correlation ID for linking to result)
	corrID := e.logToolCall(ctx, tc.Name, tc.Args)

	// Check if it's an MCP tool
	if strings.HasPrefix(tc.Name, "mcp_") {
		result, err := e.executeMCPTool(ctx, tc)
		duration := time.Since(start)
		e.logToolResult(ctx, tc.Name, tc.Args, corrID, result, err, duration)

		// MCP tools return external content - register as untrusted
		if err == nil {
			e.registerUntrustedResult(ctx, tc.Name, result, relatedBlocks)
		}
		return result, err
	}

	// Built-in tool
	if e.registry == nil {
		return "", fmt.Errorf("no tool registry")
	}

	if !e.registry.Has(tc.Name) {
		return "", fmt.Errorf("tool %q does not exist. Use one of: %s", tc.Name, strings.Join(e.registeredToolNames(), ", "))
	}

	// Enforce policy: reject tool calls that aren't enabled.
	// getAllToolDefinitions filters the LLM's view, but models can
	// hallucinate tool names from training data. This gate is the enforcement layer.
	if e.policy != nil && !e.policy.IsToolEnabled(tc.Name) {
		err := fmt.Errorf("tool %s is not enabled by policy", tc.Name)
		e.logToolResult(ctx, tc.Name, tc.Args, corrID, "", err, time.Since(start))
		e.hooks.Fire(ctx, hooks.ToolError, map[string]any{"name": tc.Name, "args": tc.Args, "error": err, "agent_role": agentID.Role})
		return "", err
	}

	// The registry validates args and runs the tool's guards before executing.
	result, err := e.registry.Execute(ctx, tc.Name, tc.Args)
	duration := time.Since(start)

	// Log the tool result
	e.logToolResult(ctx, tc.Name, tc.Args, corrID, result, err, duration)

	// Register external tool results as untrusted content
	if err == nil && isExternalTool(tc.Name) {
		e.registerUntrustedResult(ctx, tc.Name, result, relatedBlocks)
	}

	if err != nil {
		e.hooks.Fire(ctx, hooks.ToolError, map[string]any{"name": tc.Name, "args": tc.Args, "error": err, "agent_role": agentID.Role})
	}

	e.hooks.Fire(ctx, hooks.ToolCall, map[string]any{"name": tc.Name, "args": tc.Args, "result": result, "agent_role": agentID.Role})

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
func (e *Executor) registerUntrustedResult(ctx context.Context, toolName string, content string, relatedBlocks []string) {
	if e.guard == nil {
		return
	}

	// Skip empty results
	if content == "" || content == "null" {
		return
	}

	// Register as untrusted content block with taint from influencing blocks
	source := fmt.Sprintf("tool:%s", toolName)
	e.AddUntrustedContent(ctx, content, source, relatedBlocks...)
}

// toolResult holds the result of a parallel tool execution.
type toolResult struct {
	index   int
	id      string
	content string
}

// schedule says how a tool must be run within one LLM turn.
type schedule int

const (
	// parallel is the default: safe to run alongside other tools.
	parallel schedule = iota
	// serial tools have side effects or are expensive, so they run one at
	// a time, in the order the model requested them.
	serial
	// async tools are fire-and-forget: the turn does not need their result,
	// so they run in the background and report "OK".
	async
)

// scheduleOf classifies a tool by name. The executor decides scheduling
// this way because the tool interface does not declare it.
func scheduleOf(name string) schedule {
	switch name {
	case "remember", "scratchpad_write": // memory writes; result unused this turn
		return async
	case "write", "bash", "spawn_agents": // conflicting, unpredictable, or expensive
		return serial
	}
	return parallel
}

// executeToolsParallel executes multiple tool calls concurrently and returns
// messages in the original order. Async tools (remember, scratchpad_write)
// fire in background and return immediately with "OK".
// Concurrency is limited based on CPU count to avoid overwhelming resources.
func (e *Executor) executeToolsParallel(ctx context.Context, toolCalls []llm.ToolCallResponse) []llm.Message {
	if len(toolCalls) == 0 {
		return nil
	}

	// For single tool call, no need for goroutines
	if len(toolCalls) == 1 {
		tc := toolCalls[0]
		content, err := e.executeTool(ctx, tc)
		if err != nil {
			content = fmt.Sprintf("Error: %v", err)
		}
		return []llm.Message{{
			Role:       "tool",
			ToolCallID: tc.ID,
			Content:    content,
		}}
	}

	// Categorize tools: async, serialize, parallel
	var asyncCalls []int     // indices of async tools (fire-and-forget)
	var serializeCalls []int // indices of tools that must run sequentially
	var parallelCalls []int  // indices of tools that can run in parallel
	for i, tc := range toolCalls {
		switch scheduleOf(tc.Name) {
		case async:
			asyncCalls = append(asyncCalls, i)
		case serial:
			serializeCalls = append(serializeCalls, i)
		default:
			parallelCalls = append(parallelCalls, i)
		}
	}

	// Fire async tools in the background. The executor owns them (Run waits),
	// and cancellation is detached so a write in flight completes.
	for _, idx := range asyncCalls {
		tc := toolCalls[idx]
		asyncCtx, cancel := detach(ctx)
		e.background.Go(func() {
			defer cancel()
			e.executeAsyncTool(asyncCtx, tc)
		})
	}

	// Helper to run tool and return result
	runTool := func(idx int, tc llm.ToolCallResponse) toolResult {
		content, err := e.executeTool(ctx, tc)
		if err != nil {
			content = fmt.Sprintf("Error: %v", err)
		}
		return toolResult{index: idx, id: tc.ID, content: content}
	}

	// Prepare messages array
	messages := make([]llm.Message, len(toolCalls))

	// Execute parallel tools with a concurrency limit.
	if len(parallelCalls) > 0 {
		var g errgroup.Group
		g.SetLimit(concurrencyLimit)
		results := make([]toolResult, len(parallelCalls))
		for i, idx := range parallelCalls {
			tc := toolCalls[idx]
			g.Go(func() error {
				results[i] = runTool(idx, tc)
				return nil
			})
		}
		_ = g.Wait() // runTool never returns an error; failures become tool content.
		for _, r := range results {
			messages[r.index] = llm.Message{
				Role:       "tool",
				ToolCallID: r.id,
				Content:    r.content,
			}
		}
	}

	// Execute serialized tools sequentially (in order), blocking
	for _, idx := range serializeCalls {
		tc := toolCalls[idx]
		result := runTool(idx, tc)
		messages[result.index] = llm.Message{
			Role:       "tool",
			ToolCallID: result.id,
			Content:    result.content,
		}
	}

	// Fill in async tool results (already fired)
	for _, idx := range asyncCalls {
		messages[idx] = llm.Message{
			Role:       "tool",
			ToolCallID: toolCalls[idx].ID,
			Content:    "OK",
		}
	}

	return messages
}

// executeAsyncTool executes a tool asynchronously without blocking.
// Errors are logged but don't fail the LLM turn.
func (e *Executor) executeAsyncTool(ctx context.Context, tc llm.ToolCallResponse) {
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error("async tool panic", "tool", tc.Name, "panic", fmt.Sprintf("%v", r))
		}
	}()

	_, err := e.executeTool(ctx, tc)
	if err != nil {
		e.logger.Warn("async tool failed (non-blocking)", "tool", tc.Name, "error", err.Error())
	}
}

// executeMCPTool executes an MCP tool call.
func (e *Executor) executeMCPTool(ctx context.Context, tc llm.ToolCallResponse) (string, error) {
	if e.mcpManager == nil {
		return "", fmt.Errorf("no MCP manager configured")
	}

	// Parse tool name: mcp_<server>_<tool>
	parts := strings.SplitN(strings.TrimPrefix(tc.Name, "mcp_"), "_", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid MCP tool name: %s", tc.Name)
	}

	server, toolName := parts[0], parts[1]

	// Check MCP tool policy
	if e.policy != nil {
		allowed, reason, warning := e.policy.CheckMCPTool(server, toolName)
		if warning != "" {
			e.logger.Warn(warning, "server", server, "tool", toolName, "security", true)
		}
		if !allowed {
			return "", fmt.Errorf("policy denied: %s", reason)
		}
	}

	result, err := e.mcpManager.CallTool(ctx, server, toolName, tc.Args)
	if err != nil {
		return "", err
	}

	e.hooks.Fire(ctx, hooks.MCPToolCall, map[string]any{"server": server, "tool": toolName, "args": tc.Args, "result": result})

	// Extract text content
	var output strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" {
			output.WriteString(c.Text)
		}
	}

	return output.String(), nil
}
