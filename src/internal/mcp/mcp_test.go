package mcp

import (
	"context"
	"testing"
)

func TestToolDefinition(t *testing.T) {
	tool := Tool{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"arg1": map[string]interface{}{
					"type":        "string",
					"description": "First argument",
				},
			},
			"required": []string{"arg1"},
		},
	}

	if tool.Name != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", tool.Name)
	}
}

func TestServerConfig(t *testing.T) {
	config := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		Env: map[string]string{
			"DEBUG": "true",
		},
	}

	if config.Command != "npx" {
		t.Errorf("expected command 'npx', got %q", config.Command)
	}
	if len(config.Args) != 3 {
		t.Errorf("expected 3 args, got %d", len(config.Args))
	}
}

func TestManager(t *testing.T) {
	m := NewManager()

	if m.ServerCount() != 0 {
		t.Errorf("expected 0 servers, got %d", m.ServerCount())
	}

	tools := m.AllTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestManagerFindTool(t *testing.T) {
	m := NewManager()

	server, found := m.FindTool("nonexistent")
	if found {
		t.Errorf("expected tool not found, got server %q", server)
	}
}

func TestToolCallParams(t *testing.T) {
	params := ToolCallParams{
		Name: "read_file",
		Arguments: map[string]interface{}{
			"path": "/tmp/test.txt",
		},
	}

	if params.Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", params.Name)
	}
}

func TestToolCallResult(t *testing.T) {
	result := ToolCallResult{
		Content: []Content{
			{Type: "text", Text: "Hello, world!"},
		},
		IsError: false,
	}

	if len(result.Content) != 1 {
		t.Errorf("expected 1 content item, got %d", len(result.Content))
	}
	if result.Content[0].Text != "Hello, world!" {
		t.Errorf("unexpected content text")
	}
}

func TestRPCError(t *testing.T) {
	err := &RPCError{
		Code:    -32601,
		Message: "Method not found",
	}

	errStr := err.Error()
	if errStr != "RPC error -32601: Method not found" {
		t.Errorf("unexpected error string: %s", errStr)
	}
}

// Integration test - skipped without actual MCP server
func TestClientIntegration(t *testing.T) {
	t.Skip("requires actual MCP server")

	config := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-memory"},
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	t.Logf("Found %d tools", len(tools))
	for _, tool := range tools {
		t.Logf("  - %s: %s", tool.Name, tool.Description)
	}
}
