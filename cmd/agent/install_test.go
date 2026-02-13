package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

func TestInstallCmd_Defaults(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"install", "package.agent"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Install.Package != "package.agent" {
		t.Errorf("expected package 'package.agent', got %q", cli.Install.Package)
	}
	if cli.Install.NoDeps {
		t.Error("expected no-deps to be false by default")
	}
	if cli.Install.DryRun {
		t.Error("expected dry-run to be false by default")
	}
}

func TestInstallCmd_AllFlags(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{
		"install", "package.agent",
		"--target", "/opt/agents",
		"--key", "key.pub",
		"--no-deps",
		"--dry-run",
	})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Install.Target != "/opt/agents" {
		t.Errorf("expected target '/opt/agents', got %q", cli.Install.Target)
	}
	if cli.Install.Key != "key.pub" {
		t.Errorf("expected key 'key.pub', got %q", cli.Install.Key)
	}
	if !cli.Install.NoDeps {
		t.Error("expected no-deps to be true")
	}
	if !cli.Install.DryRun {
		t.Error("expected dry-run to be true")
	}
}
