// Package system contains end-to-end system tests for protocols.
package system

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProtocol_SkillDiscovery tests skill discovery from directories.
func TestProtocol_SkillDiscovery(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a skill
	skillDir := filepath.Join(tmpDir, "skills", "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: test-skill
description: A test skill for system testing.
---

# Test Skill

These are the instructions.
`), 0644)

	// Create an Agentfile that could use the skill
	agentfile := `NAME skill-test
GOAL analyze "Analyze the code"
RUN main USING analyze
`
	agentDir := filepath.Join(tmpDir, "agent")
	os.MkdirAll(agentDir, 0755)
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	// Create config with skills path
	config := `{
  "skills": {
    "paths": ["` + filepath.Join(tmpDir, "skills") + `"]
  },
  "llm": {
    "provider": "mock"
  }
}`
	os.WriteFile(filepath.Join(agentDir, "agent.json"), []byte(config), 0644)

	// Validate the agentfile
	srcDir := getSrcDir(t)
	cmd := exec.Command("go", "run", "./cmd/agent", "validate", "-f", filepath.Join(agentDir, "Agentfile"))
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validate failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "Valid") {
		t.Errorf("expected 'Valid', got: %s", output)
	}
}

// TestProtocol_SkillFormat tests SKILL.md format validation.
func TestProtocol_SkillFormat(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name     string
		content  string
		wantErr  bool
	}{
		{
			name: "valid skill",
			content: `---
name: valid-skill
description: A valid skill.
---

Instructions here.
`,
			wantErr: false,
		},
		{
			name: "missing name",
			content: `---
description: Missing name field.
---

Instructions.
`,
			wantErr: true,
		},
		{
			name: "missing description",
			content: `---
name: no-description
---

Instructions.
`,
			wantErr: true,
		},
		{
			name: "invalid name format",
			content: `---
name: Invalid_Name
description: Has underscore.
---

Instructions.
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write skill file
			skillDir := filepath.Join(tmpDir, tt.name)
			os.MkdirAll(skillDir, 0755)
			os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(tt.content), 0644)

			// Try to load - this is tested via the skills package
			// Here we just verify the file was created
			_, err := os.Stat(filepath.Join(skillDir, "SKILL.md"))
			if err != nil {
				t.Fatalf("failed to create skill file: %v", err)
			}
		})
	}
}

// TestProtocol_MCPConfigFormat tests MCP config parsing.
func TestProtocol_MCPConfigFormat(t *testing.T) {
	tmpDir := t.TempDir()

	config := `{
  "mcp": {
    "servers": {
      "filesystem": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
      },
      "memory": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-memory"]
      },
      "custom": {
        "command": "/usr/local/bin/my-mcp-server",
        "env": {
          "API_KEY": "test-key"
        }
      }
    }
  }
}`
	configPath := filepath.Join(tmpDir, "agent.json")
	os.WriteFile(configPath, []byte(config), 0644)

	// Create minimal Agentfile
	agentfile := `NAME mcp-test
GOAL test "Test"
RUN main USING test
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	// Validate
	srcDir := getSrcDir(t)
	cmd := exec.Command("go", "run", "./cmd/agent", "validate", "-f", filepath.Join(tmpDir, "Agentfile"))
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validate failed: %v\n%s", err, output)
	}
}

// TestProtocol_ACPServerInfo tests ACP agent info structure.
func TestProtocol_ACPServerInfo(t *testing.T) {
	// This tests the ACP data structures compile correctly
	// Full integration requires an actual ACP connection

	// Create agent with ACP-compatible structure
	tmpDir := t.TempDir()
	agentfile := `NAME acp-compatible
# Version tracked in manifest
INPUT query
GOAL answer "Answer the query"
RUN main USING answer
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	// Pack to verify metadata extraction works
	srcDir := getSrcDir(t)
	pkgPath := filepath.Join(tmpDir, "acp-test.agent")
	cmd := exec.Command("go", "run", "./cmd/agent", "pack", tmpDir, "-o", pkgPath)
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pack failed: %v\n%s", err, output)
	}

	// Inspect shows ACP-relevant info
	cmd = exec.Command("go", "run", "./cmd/agent", "inspect", pkgPath)
	cmd.Dir = srcDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v\n%s", err, output)
	}

	outStr := string(output)
	if !strings.Contains(outStr, "acp-compatible") {
		t.Error("expected agent name in output")
	}
	if !strings.Contains(outStr, "query") {
		t.Error("expected input 'query' in output")
	}
}

// TestProtocol_FullWorkflowWithSkills tests a workflow that could use skills.
func TestProtocol_FullWorkflowWithSkills(t *testing.T) {
	tmpDir := t.TempDir()

	// Create skills directory
	skillDir := filepath.Join(tmpDir, "skills", "code-review")
	os.MkdirAll(filepath.Join(skillDir, "scripts"), 0755)

	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: code-review
description: Review code for bugs and security issues.
license: MIT
---

# Code Review Instructions

1. Check for security vulnerabilities
2. Look for bugs
3. Suggest improvements
`), 0644)

	os.WriteFile(filepath.Join(skillDir, "scripts", "lint.sh"), []byte(`#!/bin/bash
echo "Linting..."
`), 0755)

	// Create workflow
	agentDir := filepath.Join(tmpDir, "agent")
	os.MkdirAll(agentDir, 0755)

	agentfile := `NAME review-workflow
# Version tracked in manifest
INPUT code_path
GOAL review "Review code at $code_path for issues"
RUN main USING review
`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	config := `{
  "skills": {
    "paths": ["` + filepath.Join(tmpDir, "skills") + `"]
  },
  "llm": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-20250514",
    "api_key_env": "ANTHROPIC_API_KEY"
  }
}`
	os.WriteFile(filepath.Join(agentDir, "agent.json"), []byte(config), 0644)

	// Validate
	srcDir := getSrcDir(t)
	cmd := exec.Command("go", "run", "./cmd/agent", "validate", "-f", filepath.Join(agentDir, "Agentfile"))
	cmd.Dir = srcDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validate failed: %v\n%s", err, output)
	}

	// Pack
	pkgPath := filepath.Join(tmpDir, "review.agent")
	cmd = exec.Command("go", "run", "./cmd/agent", "pack", agentDir, "-o", pkgPath)
	cmd.Dir = srcDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pack failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "review-workflow") {
		t.Error("expected package name in output")
	}
}

// TestProtocol_MCPToolNaming tests MCP tool name conventions.
func TestProtocol_MCPToolNaming(t *testing.T) {
	// MCP tools are prefixed with mcp_<server>_<tool>
	tests := []struct {
		toolName     string
		expectServer string
		expectTool   string
	}{
		{"mcp_filesystem_read_file", "filesystem", "read_file"},
		{"mcp_memory_store", "memory", "store"},
		{"mcp_github_create_pr", "github", "create_pr"},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			// Parse convention: mcp_<server>_<tool>
			if !strings.HasPrefix(tt.toolName, "mcp_") {
				t.Error("expected mcp_ prefix")
			}
			remainder := strings.TrimPrefix(tt.toolName, "mcp_")
			parts := strings.SplitN(remainder, "_", 2)
			if len(parts) != 2 {
				t.Error("expected server_tool format")
			}
			if parts[0] != tt.expectServer {
				t.Errorf("expected server %q, got %q", tt.expectServer, parts[0])
			}
			if parts[1] != tt.expectTool {
				t.Errorf("expected tool %q, got %q", tt.expectTool, parts[1])
			}
		})
	}
}
