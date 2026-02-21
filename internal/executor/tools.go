// Tool execution functions for the executor.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/vinayprograms/agentkit/llm"
)

// concurrencyLimit returns the maximum number of concurrent tool executions.
// Calculated based on CPU count with I/O-bound multiplier.
// For I/O-bound operations (web_fetch, etc.), we can oversubscribe CPUs.
var concurrencyLimit = func() int {
	cpuCount := runtime.NumCPU()
	// 4x CPU count for I/O-bound workloads (network, disk waits)
	// Minimum 4, maximum 32 to avoid overwhelming resources
	limit := cpuCount * 4
	if limit < 4 {
		limit = 4
	}
	if limit > 32 {
		limit = 32
	}
	return limit
}()

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

func (e *Executor) executeTool(ctx context.Context, tc llm.ToolCallResponse) (interface{}, error) {
	start := time.Now()
	
	// Get agent identity early for error callbacks
	agentID := getAgentIdentity(ctx)

	// Apply timeout based on tool type
	ctx, cancel := e.applyToolTimeout(ctx, tc.Name)
	if cancel != nil {
		defer cancel()
	}

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

// asyncTools are fire-and-forget tools that don't need to block the LLM turn.
// They execute in background and always return "OK" immediately.
var asyncTools = map[string]bool{
	"remember":  true, // Writes to memory - result not needed for turn
	"scratchpad_write": true, // Writes to scratchpad - result not needed for turn
}

// serializeTools must NOT be parallelized - they have side effects or are expensive.
// These run sequentially in the order the LLM requested.
var serializeTools = map[string]bool{
	"write":        true, // File writes - potential conflicts
	"bash":         true, // Arbitrary side effects - unpredictable
	"spawn_agents": true, // Expensive resource creation
}

// isAsyncTool returns true if the tool can be executed asynchronously.
func isAsyncTool(name string) bool {
	return asyncTools[name]
}

// isSerializeTool returns true if the tool must run sequentially.
func isSerializeTool(name string) bool {
	return serializeTools[name]
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

// Categorize tools: async, serialize, parallel
	var asyncCalls []int     // indices of async tools (fire-and-forget)
	var serializeCalls []int // indices of tools that must run sequentially
	var parallelCalls []int  // indices of tools that can run in parallel
	for i, tc := range toolCalls {
		switch {
		case isAsyncTool(tc.Name):
			asyncCalls = append(asyncCalls, i)
		case isSerializeTool(tc.Name):
			serializeCalls = append(serializeCalls, i)
		default:
			parallelCalls = append(parallelCalls, i)
		}
	}

	// Fire async tools in background (fire-and-forget)
	for _, idx := range asyncCalls {
		tc := toolCalls[idx]
		go e.executeAsyncTool(ctx, tc)
	}

	// Helper to run tool and return result
	runTool := func(idx int, tc llm.ToolCallResponse) toolResult {
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
		return toolResult{index: idx, id: tc.ID, content: content}
	}

	// Prepare messages array
	messages := make([]llm.Message, len(toolCalls))

	// Execute parallel tools with concurrency limit
	if len(parallelCalls) > 0 {
		sem := make(chan struct{}, concurrencyLimit)
		results := make(chan toolResult, len(parallelCalls))
		var wg sync.WaitGroup

		for _, idx := range parallelCalls {
			tc := toolCalls[idx]
			wg.Add(1)
			go func(idx int, tc llm.ToolCallResponse) {
				defer wg.Done()
				
				// Acquire semaphore (blocks if at capacity)
				sem <- struct{}{}
				defer func() { <-sem }() // Release when done
				
				results <- runTool(idx, tc)
			}(idx, tc)
		}

		// Wait for parallel tools to complete
		go func() {
			wg.Wait()
			close(results)
		}()

		// Collect parallel results
		for r := range results {
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
			e.logger.Error("async tool panic", map[string]interface{}{
				"tool":  tc.Name,
				"panic": fmt.Sprintf("%v", r),
			})
		}
	}()

	_, err := e.executeTool(ctx, tc)
	if err != nil {
		e.logger.Warn("async tool failed (non-blocking)", map[string]interface{}{
			"tool":  tc.Name,
			"error": err.Error(),
		})
	}
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
