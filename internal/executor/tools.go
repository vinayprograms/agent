// Tool execution functions for the executor.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vinayprograms/agentkit/llm"
)

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
