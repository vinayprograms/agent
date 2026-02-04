package tools

import (
	"context"
	"testing"
)

func TestSpawnAgentTool_Basic(t *testing.T) {
	// Test without spawner configured
	tool := &spawnAgentTool{}
	
	if tool.Name() != "spawn_agent" {
		t.Errorf("expected name 'spawn_agent', got %s", tool.Name())
	}
	
	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
	
	params := tool.Parameters()
	if params == nil {
		t.Fatal("expected non-nil parameters")
	}
	
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties map")
	}
	
	if _, ok := props["role"]; !ok {
		t.Error("expected 'role' parameter")
	}
	if _, ok := props["task"]; !ok {
		t.Error("expected 'task' parameter")
	}
}

func TestSpawnAgentTool_RequiresSpawner(t *testing.T) {
	tool := &spawnAgentTool{} // no spawner
	
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"role": "researcher",
		"task": "test task",
	})
	
	if err == nil {
		t.Error("expected error when spawner not configured")
	}
}

func TestSpawnAgentTool_RequiresRole(t *testing.T) {
	spawned := false
	tool := &spawnAgentTool{
		spawner: func(ctx context.Context, role, task string, outputs []string) (string, error) {
			spawned = true
			return "result", nil
		},
	}
	
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"task": "test task",
	})
	
	if err == nil {
		t.Error("expected error when role missing")
	}
	if spawned {
		t.Error("spawner should not have been called")
	}
}

func TestSpawnAgentTool_RequiresTask(t *testing.T) {
	spawned := false
	tool := &spawnAgentTool{
		spawner: func(ctx context.Context, role, task string, outputs []string) (string, error) {
			spawned = true
			return "result", nil
		},
	}
	
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"role": "researcher",
	})
	
	if err == nil {
		t.Error("expected error when task missing")
	}
	if spawned {
		t.Error("spawner should not have been called")
	}
}

func TestSpawnAgentTool_ExecutesSpawner(t *testing.T) {
	var capturedRole, capturedTask string
	tool := &spawnAgentTool{
		spawner: func(ctx context.Context, role, task string, outputs []string) (string, error) {
			capturedRole = role
			capturedTask = task
			return "sub-agent output", nil
		},
	}
	
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"role": "researcher",
		"task": "find information about X",
	})
	
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedRole != "researcher" {
		t.Errorf("expected role 'researcher', got %s", capturedRole)
	}
	if capturedTask != "find information about X" {
		t.Errorf("expected task 'find information about X', got %s", capturedTask)
	}
	if result != "sub-agent output" {
		t.Errorf("expected 'sub-agent output', got %v", result)
	}
}

func TestSpawnAgentTool_WithOutputs(t *testing.T) {
	var capturedOutputs []string
	tool := &spawnAgentTool{
		spawner: func(ctx context.Context, role, task string, outputs []string) (string, error) {
			capturedOutputs = outputs
			return `{"findings": "test", "sources": ["a", "b"]}`, nil
		},
	}
	
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"role":    "researcher",
		"task":    "find info",
		"outputs": []interface{}{"findings", "sources"},
	})
	
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedOutputs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(capturedOutputs))
	}
	if capturedOutputs[0] != "findings" || capturedOutputs[1] != "sources" {
		t.Errorf("unexpected outputs: %v", capturedOutputs)
	}
	if result != `{"findings": "test", "sources": ["a", "b"]}` {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestRegistry_HasSpawnAgent(t *testing.T) {
	registry := NewRegistry(nil)
	
	if !registry.Has("spawn_agent") {
		t.Error("expected spawn_agent to be registered by default")
	}
}

func TestRegistry_SetSpawner(t *testing.T) {
	registry := NewRegistry(nil)
	
	called := false
	registry.SetSpawner(func(ctx context.Context, role, task string, outputs []string) (string, error) {
		called = true
		return "test", nil
	})
	
	tool := registry.Get("spawn_agent")
	if tool == nil {
		t.Fatal("spawn_agent tool not found")
	}
	
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"role": "test",
		"task": "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("spawner was not called")
	}
}
