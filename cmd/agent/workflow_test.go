package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

func TestRunCmd_Defaults(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Run.File != "Agentfile" {
		t.Errorf("expected default file 'Agentfile', got %q", cli.Run.File)
	}
}

func TestRunCmd_CustomFile(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"run", "-f", "custom.agent"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Run.File != "custom.agent" {
		t.Errorf("expected 'custom.agent', got %q", cli.Run.File)
	}
}

func TestRunCmd_Inputs(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"run", "-i", "key=value", "-i", "foo=bar"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Run.Input["key"] != "value" {
		t.Errorf("expected input key=value, got %v", cli.Run.Input)
	}
	if cli.Run.Input["foo"] != "bar" {
		t.Errorf("expected input foo=bar, got %v", cli.Run.Input)
	}
}

func TestRunCmd_AllFlags(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{
		"run",
		"--config", "/path/to/config.toml",
		"--policy", "/path/to/policy.toml",
		"--workspace", "/tmp/workspace",
		"--persist-memory",
	})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Run.Config != "/path/to/config.toml" {
		t.Errorf("expected config path, got %q", cli.Run.Config)
	}
	if cli.Run.Policy != "/path/to/policy.toml" {
		t.Errorf("expected policy path, got %q", cli.Run.Policy)
	}
	if cli.Run.Workspace != "/tmp/workspace" {
		t.Errorf("expected workspace path, got %q", cli.Run.Workspace)
	}
	if !cli.Run.PersistMemory {
		t.Error("expected persist-memory to be true")
	}
}

func TestRunCmd_NoPersistMemory(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"run", "--no-persist-memory"})
	if err != nil {
		t.Fatal(err)
	}

	if !cli.Run.NoPersistMemory {
		t.Error("expected no-persist-memory to be true")
	}
}
