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

func TestLoadFile_ReadError(t *testing.T) {
	_, err := LoadFile(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error for missing Agentfile")
	}
	if !strings.Contains(err.Error(), "failed to read Agentfile") {
		t.Errorf("expected read-error message, got: %v", err)
	}
}

func TestLoadFile_GoalPromptFileMissing(t *testing.T) {
	tmpDir := t.TempDir()
	agentfile := `NAME test
GOAL main FROM goals/missing.md
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	_, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err == nil {
		t.Fatal("expected error for missing goal prompt file")
	}
	if !strings.Contains(err.Error(), "failed to load goal prompt") {
		t.Errorf("expected 'failed to load goal prompt' error, got: %v", err)
	}
}

func TestLoadFile_AgentInlinePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	agentfile := `NAME test
AGENT reviewer "You are a reviewer."
GOAL main "Test" USING reviewer
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	wf, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.Agents[0].Prompt != "You are a reviewer." {
		t.Errorf("expected inline prompt preserved, got %q", wf.Agents[0].Prompt)
	}
}

func TestLoadFile_AgentPromptFileNotMarkdown(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "agent.txt"), []byte("not markdown"), 0644)
	agentfile := `NAME test
AGENT reviewer FROM agent.txt
GOAL main "Test" USING reviewer
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	_, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err == nil {
		t.Fatal("expected error for non-.md prompt file")
	}
	if !strings.Contains(err.Error(), "must be .md") {
		t.Errorf("expected '.md' error, got: %v", err)
	}
}

func TestLoadFile_SkillPathTildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	skillsRoot := filepath.Join(home, ".agentfile-test-skills-"+t.Name())
	skillDir := filepath.Join(skillsRoot, "tilde-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	skillMd := `---
name: tilde-skill
description: Exercises tilde expansion in skill paths.
---

Instructions.
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0644)

	agentDir := t.TempDir()
	agentfile := `NAME test
AGENT tester FROM tilde-skill
GOAL main "Test" USING tester
RUN main USING main
`
	os.WriteFile(filepath.Join(agentDir, "Agentfile"), []byte(agentfile), 0644)

	rel, err := filepath.Rel(home, skillsRoot)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}

	wf, err := LoadFileWithOptions(filepath.Join(agentDir, "Agentfile"), LoadOptions{
		SkillPaths: []string{"~/" + rel},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wf.Agents[0].IsSkill {
		t.Error("expected IsSkill=true via tilde-expanded skill path")
	}
}

func TestLoadFile_SkillLoadError(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "mismatched")
	os.MkdirAll(skillDir, 0755)
	skillMd := `---
name: wrong-name
description: Name does not match directory.
---

Instructions.
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0644)

	agentfile := `NAME test
AGENT reviewer FROM mismatched
GOAL main "Test" USING reviewer
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	_, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err == nil {
		t.Fatal("expected error for skill/directory name mismatch")
	}
	if !strings.Contains(err.Error(), "failed to load skill") {
		t.Errorf("expected 'failed to load skill' error, got: %v", err)
	}
}

func TestLoadFile_SkillWithScripts(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "scripted")
	os.MkdirAll(filepath.Join(skillDir, "scripts"), 0755)
	skillMd := `---
name: scripted
description: Has scripts.
---

Instructions body.
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0644)
	os.WriteFile(filepath.Join(skillDir, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0755)

	agentfile := `NAME test
AGENT reviewer FROM scripted
GOAL main "Test" USING reviewer
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	wf, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(wf.Agents[0].Prompt, "Available Scripts") {
		t.Errorf("expected scripts section in prompt, got %q", wf.Agents[0].Prompt)
	}
}

func TestValidationError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  ValidationError
		want string
	}{
		{"with line", ValidationError{Line: 3, Msg: "boom"}, "line 3: boom"},
		{"without line", ValidationError{Msg: "boom"}, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("ValidationError.Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadFile_ParseError(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte("NAME"), 0644)

	_, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "expected identifier after NAME") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestLoadFile_ValidationError(t *testing.T) {
	tmpDir := t.TempDir()
	agentfile := `NAME test
RUN main USING undefined
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	_, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "undefined goal") {
		t.Errorf("expected validation error, got: %v", err)
	}
}

func TestLoadFile_AgentPromptFileUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	tmpDir := t.TempDir()
	promptPath := filepath.Join(tmpDir, "agent.md")
	os.WriteFile(promptPath, []byte("prompt"), 0644)
	if err := os.Chmod(promptPath, 0000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(promptPath, 0644) })

	agentfile := `NAME test
AGENT reviewer FROM agent.md
GOAL main "Test" USING reviewer
RUN main USING main
`
	os.WriteFile(filepath.Join(tmpDir, "Agentfile"), []byte(agentfile), 0644)

	_, err := LoadFile(filepath.Join(tmpDir, "Agentfile"))
	if err == nil {
		t.Fatal("expected error for unreadable prompt file")
	}
	if !strings.Contains(err.Error(), "failed to load agent prompt") {
		t.Errorf("expected 'failed to load agent prompt' error, got: %v", err)
	}
}

func TestValidation_StepUnsupervisedUnderGlobalSupervisedHuman(t *testing.T) {
	input := `SUPERVISED HUMAN
NAME test
GOAL g "g"
RUN step USING g UNSUPERVISED`

	wf, err := ParseString(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	err = Validate(wf)
	if err == nil {
		t.Fatal("expected validation error for step UNSUPERVISED under global SUPERVISED HUMAN")
	}
	if !strings.Contains(err.Error(), `step "step" cannot be UNSUPERVISED`) {
		t.Errorf("expected step-specific error, got: %v", err)
	}
}
