package setup

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinayprograms/agentkit/mcp"
)

// mcpProbeResult is the message sent after probing an MCP server
type mcpProbeResult struct {
	tools []string
	err   error
}

// probeFunc dials an MCP server and returns its discovered tool names. It is
// a consumer-defined seam: tests substitute an in-process fake instead of
// spawning a real process (or re-exec'ing the test binary as one).
type probeFunc func(ctx context.Context, cmd string, args []string) ([]string, error)

// dialMCP is the production probeFunc: it spawns the server and completes
// the MCP handshake; the client discovers the tool list as part of connecting.
func dialMCP(ctx context.Context, cmd string, args []string) ([]string, error) {
	client, err := mcp.Stdio(ctx, mcp.ServerConfig{Command: cmd, Args: args})
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	var toolNames []string
	for _, t := range client.Tools() {
		toolNames = append(toolNames, t.Name)
	}
	return toolNames, nil
}

// probeMCPServer connects to an MCP server and discovers its tools. The
// probe timeout is derived from the model's own ctx (set at construction,
// overridable via WithContext) rather than a hard-coded context.Background().
func (m Model) probeMCPServer() tea.Cmd {
	return func() tea.Msg {
		var args []string
		if m.currentMCPArgs != "" {
			args = strings.Fields(m.currentMCPArgs)
		}

		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()

		tools, err := m.probe(ctx, m.currentMCPCommand, args)
		if err != nil {
			return mcpProbeResult{err: err}
		}
		return mcpProbeResult{tools: tools}
	}
}
