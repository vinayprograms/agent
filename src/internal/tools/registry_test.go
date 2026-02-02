// Package tools provides the tool registry and built-in tools.
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/headless-agent/internal/policy"
)

// R5.1.1: Register built-in tools at startup
func TestRegistry_BuiltinTools(t *testing.T) {
	pol := policy.New()
	pol.Workspace = t.TempDir()
	reg := NewRegistry(pol)

	expectedTools := []string{"read", "write", "edit", "glob", "grep", "ls", "bash"}
	for _, name := range expectedTools {
		if reg.Get(name) == nil {
			t.Errorf("expected built-in tool %q to be registered", name)
		}
	}
}

// R5.1.2: Provide tool definitions for LLM
func TestRegistry_ToolDefinitions(t *testing.T) {
	pol := policy.New()
	pol.Workspace = t.TempDir()
	reg := NewRegistry(pol)

	defs := reg.Definitions()
	if len(defs) == 0 {
		t.Error("expected tool definitions")
	}

	// Check structure of a definition
	var found bool
	for _, def := range defs {
		if def.Name == "read" {
			found = true
			if def.Description == "" {
				t.Error("read tool should have description")
			}
			if def.Parameters == nil {
				t.Error("read tool should have parameters schema")
			}
		}
	}
	if !found {
		t.Error("read tool not in definitions")
	}
}

// R5.1.3: Look up tool by name
func TestRegistry_Lookup(t *testing.T) {
	pol := policy.New()
	pol.Workspace = t.TempDir()
	reg := NewRegistry(pol)

	tool := reg.Get("read")
	if tool == nil {
		t.Fatal("expected to find read tool")
	}

	tool = reg.Get("nonexistent")
	if tool != nil {
		t.Error("expected nil for nonexistent tool")
	}
}

// R5.1.5: Filter available tools based on policy
func TestRegistry_FilterByPolicy(t *testing.T) {
	pol := policy.New()
	pol.Workspace = t.TempDir()
	pol.Tools["bash"] = &policy.ToolPolicy{Enabled: false}
	reg := NewRegistry(pol)

	defs := reg.Definitions()
	for _, def := range defs {
		if def.Name == "bash" {
			t.Error("disabled tool should not be in definitions")
		}
	}
}

// R5.2.1: read tool - Read file contents
func TestTool_Read(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("hello world"), 0644)

	pol := policy.New()
	pol.Workspace = tmpDir
	pol.Tools["read"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{tmpDir + "/**"},
	}
	reg := NewRegistry(pol)

	tool := reg.Get("read")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": testFile,
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	if !strings.Contains(result.(string), "hello world") {
		t.Errorf("expected file content, got %v", result)
	}
}

// R5.2.2: write tool - Write content to path
func TestTool_Write(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "output.txt")

	pol := policy.New()
	pol.Workspace = tmpDir
	pol.Tools["write"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{tmpDir + "/**"},
	}
	reg := NewRegistry(pol)

	tool := reg.Get("write")
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":    testFile,
		"content": "new content",
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	content, _ := os.ReadFile(testFile)
	if string(content) != "new content" {
		t.Errorf("expected 'new content', got %s", content)
	}
}

// R5.2.2: write tool - Create parent directories
func TestTool_Write_CreateDirs(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "a", "b", "c", "file.txt")

	pol := policy.New()
	pol.Workspace = tmpDir
	pol.Tools["write"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{tmpDir + "/**"},
	}
	reg := NewRegistry(pol)

	tool := reg.Get("write")
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":    testFile,
		"content": "nested",
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("file should exist")
	}
}

// R5.2.3: edit tool - Find and replace
func TestTool_Edit(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("hello world"), 0644)

	pol := policy.New()
	pol.Workspace = tmpDir
	pol.Tools["edit"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{tmpDir + "/**"},
	}
	pol.Tools["read"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{tmpDir + "/**"},
	}
	reg := NewRegistry(pol)

	tool := reg.Get("edit")
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":    testFile,
		"old":     "world",
		"new":     "universe",
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	content, _ := os.ReadFile(testFile)
	if string(content) != "hello universe" {
		t.Errorf("expected 'hello universe', got %s", content)
	}
}

// R5.2.3: edit tool - Report if pattern not found
func TestTool_Edit_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("hello world"), 0644)

	pol := policy.New()
	pol.Workspace = tmpDir
	pol.Tools["edit"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{tmpDir + "/**"},
	}
	pol.Tools["read"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{tmpDir + "/**"},
	}
	reg := NewRegistry(pol)

	tool := reg.Get("edit")
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"path":    testFile,
		"old":     "nonexistent",
		"new":     "replacement",
	})
	if err == nil {
		t.Error("expected error for pattern not found")
	}
}

// R5.2.4: glob tool - Pattern-based file search
func TestTool_Glob(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "c.txt"), []byte(""), 0644)

	pol := policy.New()
	pol.Workspace = tmpDir
	reg := NewRegistry(pol)

	tool := reg.Get("glob")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"pattern": filepath.Join(tmpDir, "*.go"),
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	files := result.([]string)
	if len(files) != 2 {
		t.Errorf("expected 2 .go files, got %d", len(files))
	}
}

// R5.2.6: ls tool - List directory contents
func TestTool_Ls(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte(""), 0644)
	os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte(""), 0644)
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)

	pol := policy.New()
	pol.Workspace = tmpDir
	reg := NewRegistry(pol)

	tool := reg.Get("ls")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": tmpDir,
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	entries := result.([]DirEntry)
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
}

// R5.3.1: bash tool - Execute shell command
func TestTool_Bash(t *testing.T) {
	pol := policy.New()
	pol.Workspace = t.TempDir()
	pol.Tools["bash"] = &policy.ToolPolicy{
		Enabled:   true,
		Allowlist: []string{"echo *"},
	}
	reg := NewRegistry(pol)

	tool := reg.Get("bash")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	execResult := result.(*ExecResult)
	if !strings.Contains(execResult.Stdout, "hello") {
		t.Errorf("expected 'hello' in output, got %s", execResult.Stdout)
	}
}

// R5.3.1: bash tool - Enforce allowlist from policy
func TestTool_Bash_PolicyDeny(t *testing.T) {
	pol := policy.New()
	pol.Workspace = t.TempDir()
	pol.Tools["bash"] = &policy.ToolPolicy{
		Enabled:   true,
		Allowlist: []string{"echo *"},
	}
	reg := NewRegistry(pol)

	tool := reg.Get("bash")
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"command": "rm -rf /",
	})
	if err == nil {
		t.Error("expected policy denial error")
	}
	if !strings.Contains(err.Error(), "policy") || !strings.Contains(err.Error(), "denied") {
		t.Errorf("error should mention policy denial: %v", err)
	}
}

// Test security: read tool denies paths outside workspace
func TestTool_Read_SecurityDeny(t *testing.T) {
	pol := policy.New()
	pol.Workspace = "/safe/workspace"
	pol.Tools["read"] = &policy.ToolPolicy{
		Enabled: true,
		Allow:   []string{"/safe/workspace/**"},
	}
	reg := NewRegistry(pol)

	tool := reg.Get("read")
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": "/etc/passwd",
	})
	if err == nil {
		t.Error("expected security denial")
	}
}
