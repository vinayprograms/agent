// Package mcp provides MCP (Model Context Protocol) client support.
package mcp

import (
	"context"
	"fmt"
	"sync"
)

// Manager manages multiple MCP server connections.
type Manager struct {
	clients map[string]*Client
	mu      sync.RWMutex
}

// NewManager creates a new MCP manager.
func NewManager() *Manager {
	return &Manager{
		clients: make(map[string]*Client),
	}
}

// Connect connects to an MCP server.
func (m *Manager) Connect(ctx context.Context, name string, config ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clients[name]; exists {
		return fmt.Errorf("server %q already connected", name)
	}

	client, err := NewClient(config)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	if err := client.Initialize(ctx); err != nil {
		client.Close()
		return fmt.Errorf("failed to initialize: %w", err)
	}

	// Fetch tools
	if _, err := client.ListTools(ctx); err != nil {
		client.Close()
		return fmt.Errorf("failed to list tools: %w", err)
	}

	m.clients[name] = client
	return nil
}

// Disconnect disconnects from an MCP server.
func (m *Manager) Disconnect(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[name]
	if !ok {
		return fmt.Errorf("server %q not connected", name)
	}

	delete(m.clients, name)
	return client.Close()
}

// AllTools returns all tools from all connected servers.
func (m *Manager) AllTools() []ToolWithServer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var tools []ToolWithServer
	for server, client := range m.clients {
		for _, tool := range client.Tools() {
			tools = append(tools, ToolWithServer{
				Server: server,
				Tool:   tool,
			})
		}
	}
	return tools
}

// ToolWithServer pairs a tool with its server name.
type ToolWithServer struct {
	Server string
	Tool   Tool
}

// CallTool calls a tool on a specific server.
func (m *Manager) CallTool(ctx context.Context, server, tool string, args map[string]interface{}) (*ToolCallResult, error) {
	m.mu.RLock()
	client, ok := m.clients[server]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("server %q not connected", server)
	}

	return client.CallTool(ctx, tool, args)
}

// FindTool finds which server has a tool.
func (m *Manager) FindTool(name string) (server string, found bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for srv, client := range m.clients {
		for _, tool := range client.Tools() {
			if tool.Name == name {
				return srv, true
			}
		}
	}
	return "", false
}

// Close disconnects all servers.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			lastErr = err
		}
		delete(m.clients, name)
	}
	return lastErr
}

// ServerCount returns the number of connected servers.
func (m *Manager) ServerCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients)
}

// Servers returns the names of connected servers.
func (m *Manager) Servers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	return names
}
