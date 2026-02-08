package security

import (
	"strings"
	"testing"
)

func TestSecurityModes(t *testing.T) {
	tests := []struct {
		name     string
		mode     Mode
		expected string
	}{
		{"default mode", ModeDefault, "default"},
		{"paranoid mode", ModeParanoid, "paranoid"},
		{"research mode", ModeResearch, "research"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.mode) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(tt.mode))
			}
		})
	}
}

func TestSecuritySupervisor_ResearchPrompt(t *testing.T) {
	// Test that research mode supervisor builds research-aware prompt
	scope := "authorized pentest of internal lab network"
	supervisor := NewSecuritySupervisor(nil, ModeResearch, scope)

	if supervisor.mode != ModeResearch {
		t.Errorf("expected mode=research, got %s", supervisor.mode)
	}

	if supervisor.researchScope != scope {
		t.Errorf("expected scope=%q, got %q", scope, supervisor.researchScope)
	}

	// Test that buildResearchSystemPrompt includes the scope
	prompt := supervisor.buildResearchSystemPrompt()
	if prompt == "" {
		t.Error("expected non-empty research system prompt")
	}

	if !strings.Contains(prompt, scope) {
		t.Error("expected research prompt to contain scope")
	}

	if !strings.Contains(prompt, "AUTHORIZED SECURITY RESEARCH") {
		t.Error("expected research prompt to indicate authorized research")
	}

	if !strings.Contains(prompt, "pentesting") && !strings.Contains(prompt, "Pentesting") {
		t.Error("expected research prompt to mention pentesting")
	}
}

func TestSecuritySupervisor_DefaultPromptNotResearch(t *testing.T) {
	// Test that default mode doesn't use research prompt
	supervisor := NewSecuritySupervisor(nil, ModeDefault, "")

	if supervisor.mode != ModeDefault {
		t.Errorf("expected mode=default, got %s", supervisor.mode)
	}

	// Default mode should have empty research scope
	if supervisor.researchScope != "" {
		t.Errorf("expected empty scope, got %q", supervisor.researchScope)
	}
}

func TestSecuritySupervisor_ParanoidModeNotResearch(t *testing.T) {
	// Test that paranoid mode doesn't use research prompt
	supervisor := NewSecuritySupervisor(nil, ModeParanoid, "")

	if supervisor.mode != ModeParanoid {
		t.Errorf("expected mode=paranoid, got %s", supervisor.mode)
	}
}
