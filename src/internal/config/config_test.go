// Package config provides configuration loading and management.
package config

import (
	"os"
	"path/filepath"
	"testing"
)

// R10.1.1: Load config from JSON file
func TestConfig_LoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.json")
	os.WriteFile(configPath, []byte(`{
		"agent": {
			"id": "test-agent",
			"workspace": "/workspace"
		},
		"llm": {
			"provider": "anthropic",
			"model": "claude-3-5-sonnet",
			"api_key_env": "ANTHROPIC_API_KEY",
			"max_tokens": 4096
		}
	}`), 0644)

	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if cfg.Agent.ID != "test-agent" {
		t.Errorf("expected id 'test-agent', got %s", cfg.Agent.ID)
	}
	if cfg.Agent.Workspace != "/workspace" {
		t.Errorf("expected workspace '/workspace', got %s", cfg.Agent.Workspace)
	}
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %s", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "claude-3-5-sonnet" {
		t.Errorf("expected model 'claude-3-5-sonnet', got %s", cfg.LLM.Model)
	}
	if cfg.LLM.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("expected api_key_env 'ANTHROPIC_API_KEY', got %s", cfg.LLM.APIKeyEnv)
	}
	if cfg.LLM.MaxTokens != 4096 {
		t.Errorf("expected max_tokens 4096, got %d", cfg.LLM.MaxTokens)
	}
}

// R10.1.3: Default to agent.json in current directory
func TestConfig_LoadDefault(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tmpDir)

	os.WriteFile("agent.json", []byte(`{
		"agent": {"id": "default-agent"}
	}`), 0644)

	cfg, err := LoadDefault()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if cfg.Agent.ID != "default-agent" {
		t.Errorf("expected id 'default-agent', got %s", cfg.Agent.ID)
	}
}

// R10.2.1-R10.2.13: All config sections
func TestConfig_AllSections(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.json")
	os.WriteFile(configPath, []byte(`{
		"agent": {
			"id": "full-agent",
			"workspace": "/home/agent/workspace"
		},
		"llm": {
			"provider": "openai",
			"model": "gpt-4o",
			"api_key_env": "OPENAI_API_KEY",
			"max_tokens": 8192
		},
		"web": {
			"gateway_url": "https://gateway.example.com",
			"gateway_token_env": "GATEWAY_TOKEN"
		},
		"telemetry": {
			"enabled": true,
			"endpoint": "https://telemetry.example.com",
			"protocol": "otlp"
		},
		"session": {
			"store": "sqlite",
			"path": "/data/sessions.db"
		}
	}`), 0644)

	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	// Agent section
	if cfg.Agent.ID != "full-agent" {
		t.Errorf("agent.id: expected 'full-agent', got %s", cfg.Agent.ID)
	}
	if cfg.Agent.Workspace != "/home/agent/workspace" {
		t.Errorf("agent.workspace: expected '/home/agent/workspace', got %s", cfg.Agent.Workspace)
	}

	// LLM section
	if cfg.LLM.Provider != "openai" {
		t.Errorf("llm.provider: expected 'openai', got %s", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "gpt-4o" {
		t.Errorf("llm.model: expected 'gpt-4o', got %s", cfg.LLM.Model)
	}
	if cfg.LLM.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("llm.api_key_env: expected 'OPENAI_API_KEY', got %s", cfg.LLM.APIKeyEnv)
	}
	if cfg.LLM.MaxTokens != 8192 {
		t.Errorf("llm.max_tokens: expected 8192, got %d", cfg.LLM.MaxTokens)
	}

	// Web section
	if cfg.Web.GatewayURL != "https://gateway.example.com" {
		t.Errorf("web.gateway_url: expected 'https://gateway.example.com', got %s", cfg.Web.GatewayURL)
	}
	if cfg.Web.GatewayTokenEnv != "GATEWAY_TOKEN" {
		t.Errorf("web.gateway_token_env: expected 'GATEWAY_TOKEN', got %s", cfg.Web.GatewayTokenEnv)
	}

	// Telemetry section
	if !cfg.Telemetry.Enabled {
		t.Error("telemetry.enabled: expected true")
	}
	if cfg.Telemetry.Endpoint != "https://telemetry.example.com" {
		t.Errorf("telemetry.endpoint: expected 'https://telemetry.example.com', got %s", cfg.Telemetry.Endpoint)
	}
	if cfg.Telemetry.Protocol != "otlp" {
		t.Errorf("telemetry.protocol: expected 'otlp', got %s", cfg.Telemetry.Protocol)
	}

	// Session section
	if cfg.Session.Store != "sqlite" {
		t.Errorf("session.store: expected 'sqlite', got %s", cfg.Session.Store)
	}
	if cfg.Session.Path != "/data/sessions.db" {
		t.Errorf("session.path: expected '/data/sessions.db', got %s", cfg.Session.Path)
	}
}

// Test defaults
func TestConfig_Defaults(t *testing.T) {
	cfg := New()

	if cfg.LLM.MaxTokens != 4096 {
		t.Errorf("default max_tokens should be 4096, got %d", cfg.LLM.MaxTokens)
	}
	if cfg.Session.Store != "file" {
		t.Errorf("default store should be 'file', got %s", cfg.Session.Store)
	}
	if cfg.Telemetry.Protocol != "noop" {
		t.Errorf("default telemetry protocol should be 'noop', got %s", cfg.Telemetry.Protocol)
	}
}

// Test file not found
func TestConfig_FileNotFound(t *testing.T) {
	_, err := LoadFile("/nonexistent/path/agent.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// Test invalid JSON
func TestConfig_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.json")
	os.WriteFile(configPath, []byte(`{invalid`), 0644)

	_, err := LoadFile(configPath)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// Test GetAPIKey from environment
func TestConfig_GetAPIKey(t *testing.T) {
	os.Setenv("TEST_API_KEY", "secret123")
	defer os.Unsetenv("TEST_API_KEY")

	cfg := New()
	cfg.LLM.APIKeyEnv = "TEST_API_KEY"

	key := cfg.GetAPIKey()
	if key != "secret123" {
		t.Errorf("expected 'secret123', got %s", key)
	}
}

// Test GetAPIKey uses default env var when api_key_env not set
func TestConfig_GetAPIKey_Default(t *testing.T) {
	os.Setenv("ANTHROPIC_API_KEY", "default-anthropic-key")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	cfg := New()
	cfg.LLM.Provider = "anthropic"
	// api_key_env not set - should use default ANTHROPIC_API_KEY

	key := cfg.GetAPIKey()
	if key != "default-anthropic-key" {
		t.Errorf("expected 'default-anthropic-key', got %s", key)
	}
}

// Test DefaultAPIKeyEnv returns correct env var for each provider
func TestDefaultAPIKeyEnv(t *testing.T) {
	tests := []struct {
		provider string
		expected string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"google", "GOOGLE_API_KEY"},
		{"mistral", "MISTRAL_API_KEY"},
		{"groq", "GROQ_API_KEY"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		result := DefaultAPIKeyEnv(tt.provider)
		if result != tt.expected {
			t.Errorf("DefaultAPIKeyEnv(%q) = %q, want %q", tt.provider, result, tt.expected)
		}
	}
}

// Test GetGatewayToken from environment
func TestConfig_GetGatewayToken(t *testing.T) {
	os.Setenv("TEST_GATEWAY_TOKEN", "gateway456")
	defer os.Unsetenv("TEST_GATEWAY_TOKEN")

	cfg := New()
	cfg.Web.GatewayTokenEnv = "TEST_GATEWAY_TOKEN"

	token := cfg.GetGatewayToken()
	if token != "gateway456" {
		t.Errorf("expected 'gateway456', got %s", token)
	}
}
