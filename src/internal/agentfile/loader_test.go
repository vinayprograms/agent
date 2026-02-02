package agentfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFile_SmartResolution_MarkdownFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create agent prompt file
	agentsDir := filepath.Join(tmpDir, "agents")
	os.MkdirAll(agentsDir, 0755)
	os.WriteFile(filepath.Join(agentsDir, "critic.md"), []byte("You are a critic."), 0644)

	// Create Agentfile
	agentfile := `NAME test
AGENT critic FROM agents/critic.md
GOAL main "Test"
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	wf, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(wf.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(wf.Agents))
	}

	if wf.Agents[0].Prompt != "You are a critic." {
		t.Errorf("expected prompt content, got %q", wf.Agents[0].Prompt)
	}

	if wf.Agents[0].IsSkill {
		t.Error("expected IsSkill=false for .md file")
	}
}

func TestLoadFile_SmartResolution_SkillDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create skill directory
	skillDir := filepath.Join(tmpDir, "skills", "code-review")
	os.MkdirAll(skillDir, 0755)
	skillMd := `---
name: code-review
description: Review code for quality and bugs.
---

# Instructions

1. Check for bugs
2. Suggest improvements
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0644)

	// Create Agentfile referencing skill directory
	agentfile := `NAME test
AGENT reviewer FROM skills/code-review
GOAL main "Test"
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	wf, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(wf.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(wf.Agents))
	}

	if !wf.Agents[0].IsSkill {
		t.Error("expected IsSkill=true for skill directory")
	}

	if !strings.Contains(wf.Agents[0].Prompt, "Review code") {
		t.Errorf("expected skill description in prompt, got %q", wf.Agents[0].Prompt)
	}
}

func TestLoadFile_SmartResolution_SkillFromPaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Create skill in a separate skills directory
	globalSkillsDir := filepath.Join(tmpDir, "global-skills")
	skillDir := filepath.Join(globalSkillsDir, "testing")
	os.MkdirAll(skillDir, 0755)
	skillMd := `---
name: testing
description: Write comprehensive tests.
---

# Instructions

Write unit tests.
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0644)

	// Create Agentfile referencing skill by name only
	agentDir := filepath.Join(tmpDir, "agent")
	os.MkdirAll(agentDir, 0755)
	agentfile := `NAME test
AGENT tester FROM testing
GOAL main "Test"
RUN main USING main
`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	// Load with skill paths
	wf, err := LoadFileWithOptions(filepath.Join(agentDir, "Agentfile"), LoadOptions{
		SkillPaths: []string{globalSkillsDir},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !wf.Agents[0].IsSkill {
		t.Error("expected IsSkill=true")
	}

	if !strings.Contains(wf.Agents[0].Prompt, "Write comprehensive tests") {
		t.Errorf("expected skill description, got %q", wf.Agents[0].Prompt)
	}
}

func TestLoadFile_SmartResolution_DirectoryWithoutSkillMd(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory without SKILL.md
	invalidDir := filepath.Join(tmpDir, "not-a-skill")
	os.MkdirAll(invalidDir, 0755)
	os.WriteFile(filepath.Join(invalidDir, "README.md"), []byte("Not a skill"), 0644)

	// Create Agentfile
	agentfile := `NAME test
AGENT invalid FROM not-a-skill
GOAL main "Test"
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	_, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err == nil {
		t.Error("expected error for directory without SKILL.md")
	}
	if !strings.Contains(err.Error(), "not a valid skill") {
		t.Errorf("expected 'not a valid skill' error, got: %v", err)
	}
}

func TestLoadFile_SmartResolution_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	agentfile := `NAME test
AGENT missing FROM nonexistent
GOAL main "Test"
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	_, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err == nil {
		t.Error("expected error for missing agent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestLoadFileWithOptions(t *testing.T) {
	tmpDir := t.TempDir()

	// Create minimal valid Agentfile without agents
	agentfile := `NAME test
GOAL main "Test"
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	wf, err := LoadFileWithOptions(filepath.Join(tmpDir, "Agentfile"), LoadOptions{
		SkillPaths: []string{"/some/path"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wf.Name != "test" {
		t.Errorf("expected name 'test', got %q", wf.Name)
	}
}
