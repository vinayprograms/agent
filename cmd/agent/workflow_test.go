package main

import (
	"os"
	"testing"
)

func TestParseWorkflow_DefaultAgentfile(t *testing.T) {
	// Create temp dir with Agentfile
	dir := t.TempDir()
	agentfile := dir + "/Agentfile"
	if err := os.WriteFile(agentfile, []byte("NAME test\nGOAL main\n  OUTPUT result\nENDGOAL"), 0644); err != nil {
		t.Fatal(err)
	}

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	w := parseWorkflow(nil)
	if w.agentfilePath != "Agentfile" {
		t.Errorf("expected Agentfile path 'Agentfile', got %q", w.agentfilePath)
	}
}

func TestParseWorkflow_CustomPath(t *testing.T) {
	dir := t.TempDir()
	agentfile := dir + "/custom.agent"
	if err := os.WriteFile(agentfile, []byte("NAME test\nGOAL main\n  OUTPUT result\nENDGOAL"), 0644); err != nil {
		t.Fatal(err)
	}

	w := parseWorkflow([]string{"-f", agentfile})
	if w.agentfilePath != agentfile {
		t.Errorf("expected path %q, got %q", agentfile, w.agentfilePath)
	}
}

func TestParseFlags_Inputs(t *testing.T) {
	w := &workflow{inputs: make(map[string]string)}
	w.parseFlags([]string{"--input", "key=value", "--input=foo=bar"})

	if w.inputs["key"] != "value" {
		t.Errorf("expected input key=value, got %v", w.inputs)
	}
	if w.inputs["foo"] != "bar" {
		t.Errorf("expected input foo=bar, got %v", w.inputs)
	}
}

func TestParseFlags_Config(t *testing.T) {
	w := &workflow{inputs: make(map[string]string)}
	w.parseFlags([]string{"--config", "/path/to/config.toml"})
	if w.configPath != "/path/to/config.toml" {
		t.Errorf("expected config path, got %q", w.configPath)
	}
}

func TestParseFlags_Policy(t *testing.T) {
	w := &workflow{inputs: make(map[string]string)}
	w.parseFlags([]string{"--policy=/path/to/policy.toml"})
	if w.policyPath != "/path/to/policy.toml" {
		t.Errorf("expected policy path, got %q", w.policyPath)
	}
}

func TestParseFlags_Workspace(t *testing.T) {
	w := &workflow{inputs: make(map[string]string)}
	w.parseFlags([]string{"--workspace", "/tmp/workspace"})
	if w.workspacePath != "/tmp/workspace" {
		t.Errorf("expected workspace path, got %q", w.workspacePath)
	}
}

func TestParseFlags_PersistMemory(t *testing.T) {
	tests := []struct {
		flag     string
		expected *bool
	}{
		{"--persist-memory", ptrBool(true)},
		{"--no-persist-memory", ptrBool(false)},
	}
	for _, tt := range tests {
		w := &workflow{inputs: make(map[string]string)}
		w.parseFlags([]string{tt.flag})
		if w.persistMemoryOverride == nil {
			t.Errorf("%s: expected override, got nil", tt.flag)
		} else if *w.persistMemoryOverride != *tt.expected {
			t.Errorf("%s: expected %v, got %v", tt.flag, *tt.expected, *w.persistMemoryOverride)
		}
	}
}

func ptrBool(v bool) *bool { return &v }

func TestParseInput(t *testing.T) {
	w := &workflow{inputs: make(map[string]string)}
	w.parseInput("key=value=with=equals")
	if w.inputs["key"] != "value=with=equals" {
		t.Errorf("expected value with equals, got %q", w.inputs["key"])
	}
}
