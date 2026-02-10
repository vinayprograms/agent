package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	content := `---
name: test-skill
description: A test skill for unit testing.
license: MIT
metadata:
  author: test
  version: "1.0"
---

# Test Skill Instructions

This is the body of the skill with instructions.

## Steps

1. Do this
2. Then that
`

	skill, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if skill.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got %q", skill.Name)
	}
	if skill.Description != "A test skill for unit testing." {
		t.Errorf("unexpected description: %q", skill.Description)
	}
	if skill.License != "MIT" {
		t.Errorf("expected license 'MIT', got %q", skill.License)
	}
	if skill.Metadata["author"] != "test" {
		t.Errorf("expected author 'test', got %q", skill.Metadata["author"])
	}
	if skill.Instructions == "" {
		t.Error("expected instructions to be set")
	}
}

func TestParseMissingName(t *testing.T) {
	content := `---
description: No name provided
---

Instructions here.
`

	_, err := Parse(content)
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestParseMissingDescription(t *testing.T) {
	content := `---
name: no-description
---

Instructions here.
`

	_, err := Parse(content)
	if err == nil {
		t.Error("expected error for missing description")
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"valid-name", false},
		{"test123", false},
		{"a", false},
		{"", true},
		{"UPPERCASE", true},
		{"-starts-with-hyphen", true},
		{"ends-with-hyphen-", true},
		{"has--double-hyphen", true},
		{"has space", true},
		{"has_underscore", true},
	}

	for _, tt := range tests {
		err := validateName(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "my-skill")
	os.MkdirAll(skillDir, 0755)

	skillContent := `---
name: my-skill
description: A skill loaded from disk.
---

Instructions for my-skill.
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644)

	skill, err := Load(skillDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if skill.Name != "my-skill" {
		t.Errorf("expected name 'my-skill', got %q", skill.Name)
	}
	if skill.Path != skillDir {
		t.Errorf("expected path %q, got %q", skillDir, skill.Path)
	}
}

func TestLoadNameMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "actual-dir")
	os.MkdirAll(skillDir, 0755)

	skillContent := `---
name: different-name
description: Name doesn't match directory.
---

Instructions.
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644)

	_, err := Load(skillDir)
	if err == nil {
		t.Error("expected error for name mismatch")
	}
}

func TestDiscover(t *testing.T) {
	tmpDir := t.TempDir()

	// Create skill 1
	skill1Dir := filepath.Join(tmpDir, "skill-one")
	os.MkdirAll(skill1Dir, 0755)
	os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(`---
name: skill-one
description: First skill.
---

Instructions.
`), 0644)

	// Create skill 2
	skill2Dir := filepath.Join(tmpDir, "skill-two")
	os.MkdirAll(skill2Dir, 0755)
	os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(`---
name: skill-two
description: Second skill.
---

Instructions.
`), 0644)

	// Create non-skill directory
	otherDir := filepath.Join(tmpDir, "not-a-skill")
	os.MkdirAll(otherDir, 0755)
	os.WriteFile(filepath.Join(otherDir, "README.md"), []byte("not a skill"), 0644)

	refs, err := Discover(tmpDir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(refs) != 2 {
		t.Errorf("expected 2 skills, got %d", len(refs))
	}

	names := make(map[string]bool)
	for _, ref := range refs {
		names[ref.Name] = true
	}

	if !names["skill-one"] {
		t.Error("expected skill-one to be discovered")
	}
	if !names["skill-two"] {
		t.Error("expected skill-two to be discovered")
	}
}

func TestDiscoverEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	refs, err := Discover(tmpDir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(refs) != 0 {
		t.Errorf("expected 0 skills, got %d", len(refs))
	}
}

func TestDiscoverNonexistent(t *testing.T) {
	refs, err := Discover("/nonexistent/path")
	if err != nil {
		t.Fatalf("Discover should not error for nonexistent path: %v", err)
	}

	if refs != nil && len(refs) != 0 {
		t.Errorf("expected nil or empty refs, got %d", len(refs))
	}
}

func TestSkillScripts(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "script-skill")
	scriptsDir := filepath.Join(skillDir, "scripts")
	os.MkdirAll(scriptsDir, 0755)

	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: script-skill
description: A skill with scripts.
---

Use the scripts.
`), 0644)

	os.WriteFile(filepath.Join(scriptsDir, "run.sh"), []byte("#!/bin/bash\necho hello"), 0755)
	os.WriteFile(filepath.Join(scriptsDir, "process.py"), []byte("print('hello')"), 0644)

	skill, err := Load(skillDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	scripts, err := skill.ListScripts()
	if err != nil {
		t.Fatalf("ListScripts: %v", err)
	}

	if len(scripts) != 2 {
		t.Errorf("expected 2 scripts, got %d", len(scripts))
	}

	scriptPath := skill.ScriptPath("run.sh")
	expectedPath := filepath.Join(scriptsDir, "run.sh")
	if scriptPath != expectedPath {
		t.Errorf("expected script path %q, got %q", expectedPath, scriptPath)
	}
}

func TestSkillReferences(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "ref-skill")
	refsDir := filepath.Join(skillDir, "references")
	os.MkdirAll(refsDir, 0755)

	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: ref-skill
description: A skill with references.
---

See references.
`), 0644)

	os.WriteFile(filepath.Join(refsDir, "REFERENCE.md"), []byte("# Reference\n\nDetailed docs."), 0644)

	skill, err := Load(skillDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	content, err := skill.ReadReference("REFERENCE.md")
	if err != nil {
		t.Fatalf("ReadReference: %v", err)
	}

	if content != "# Reference\n\nDetailed docs." {
		t.Errorf("unexpected reference content: %q", content)
	}
}
