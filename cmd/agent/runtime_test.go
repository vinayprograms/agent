package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/security"
)

func TestResolveStoragePath_Default(t *testing.T) {
	home, _ := os.UserHomeDir()
	rt := &runtime{
		cfg: &config.Config{},
		wf:  &agentfile.Workflow{Name: "test"},
	}
	rt.resolveStoragePath()

	expected := filepath.Join(home, ".local", "grid")
	if rt.storagePath != expected {
		t.Errorf("expected %q, got %q", expected, rt.storagePath)
	}
	if rt.sessionPath != filepath.Join(expected, "sessions", "test") {
		t.Errorf("unexpected session path: %q", rt.sessionPath)
	}
}

func TestResolveStoragePath_Custom(t *testing.T) {
	rt := &runtime{
		cfg: &config.Config{Storage: config.StorageConfig{Path: "/custom/path"}},
		wf:  &agentfile.Workflow{Name: "myworkflow"},
	}
	rt.resolveStoragePath()

	if rt.storagePath != "/custom/path" {
		t.Errorf("expected /custom/path, got %q", rt.storagePath)
	}
}

func TestResolveStoragePath_TildeExpansion(t *testing.T) {
	home, _ := os.UserHomeDir()
	rt := &runtime{
		cfg: &config.Config{Storage: config.StorageConfig{Path: "~/mydata"}},
		wf:  &agentfile.Workflow{Name: "test"},
	}
	rt.resolveStoragePath()

	expected := filepath.Join(home, "mydata")
	if rt.storagePath != expected {
		t.Errorf("expected %q, got %q", expected, rt.storagePath)
	}
}

func TestDetermineSecurityConfig_Default(t *testing.T) {
	rt := &runtime{
		cfg: &config.Config{},
		wf:  &agentfile.Workflow{},
	}
	mode, scope, trust := rt.determineSecurityConfig()

	if mode != security.ModeDefault {
		t.Errorf("expected default mode, got %v", mode)
	}
	if scope != "" {
		t.Errorf("expected empty scope, got %q", scope)
	}
	if trust != security.TrustUntrusted {
		t.Errorf("expected untrusted, got %v", trust)
	}
}

func TestDetermineSecurityConfig_Paranoid(t *testing.T) {
	rt := &runtime{
		cfg: &config.Config{Security: config.SecurityConfig{Mode: "paranoid"}},
		wf:  &agentfile.Workflow{},
	}
	mode, _, _ := rt.determineSecurityConfig()

	if mode != security.ModeParanoid {
		t.Errorf("expected paranoid mode, got %v", mode)
	}
}

func TestDetermineSecurityConfig_Research(t *testing.T) {
	rt := &runtime{
		cfg: &config.Config{},
		wf:  &agentfile.Workflow{SecurityMode: "research", SecurityScope: "OWASP Top 10"},
	}
	mode, scope, _ := rt.determineSecurityConfig()

	if mode != security.ModeResearch {
		t.Errorf("expected research mode, got %v", mode)
	}
	if scope != "OWASP Top 10" {
		t.Errorf("expected scope 'OWASP Top 10', got %q", scope)
	}
}

func TestDetermineSecurityConfig_TrustLevels(t *testing.T) {
	tests := []struct {
		userTrust string
		expected  security.TrustLevel
	}{
		{"", security.TrustUntrusted},
		{"trusted", security.TrustTrusted},
		{"vetted", security.TrustVetted},
	}
	for _, tt := range tests {
		rt := &runtime{
			cfg: &config.Config{Security: config.SecurityConfig{UserTrust: tt.userTrust}},
			wf:  &agentfile.Workflow{},
		}
		_, _, trust := rt.determineSecurityConfig()
		if trust != tt.expected {
			t.Errorf("userTrust=%q: expected %v, got %v", tt.userTrust, tt.expected, trust)
		}
	}
}

func TestAddCloserAndCleanup(t *testing.T) {
	var calls []int
	rt := &runtime{}

	rt.addCloser(func() { calls = append(calls, 1) })
	rt.addCloser(func() { calls = append(calls, 2) })
	rt.addCloser(func() { calls = append(calls, 3) })

	rt.cleanup()

	// Should run in reverse order
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	if calls[0] != 3 || calls[1] != 2 || calls[2] != 1 {
		t.Errorf("expected [3,2,1], got %v", calls)
	}
}

func TestSetupBashChecker_FailClose(t *testing.T) {
	pol := policy.New()
	rt := &runtime{
		pol:      pol,
		registry: nil, // Would need mock registry for full test
	}
	_ = rt // Smoke test - full test needs registry mock
}
