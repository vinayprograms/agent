package main

import (
	"os"
	"testing"

	"github.com/alecthomas/kong"
)

func TestKeygenCmd_Default(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"keygen"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Keygen.Output != "agent-key" {
		t.Errorf("expected default output 'agent-key', got %q", cli.Keygen.Output)
	}
}

func TestKeygenCmd_CustomOutput(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"keygen", "-o", "custom-key"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Keygen.Output != "custom-key" {
		t.Errorf("expected output 'custom-key', got %q", cli.Keygen.Output)
	}
}

func TestCheckKeyPaths_Exists(t *testing.T) {
	dir := t.TempDir()
	privPath := dir + "/test.pem"
	pubPath := dir + "/test.pub"

	// Create priv file
	os.WriteFile(privPath, []byte("test"), 0600)

	err := checkKeyPaths(privPath, pubPath)
	if err == nil {
		t.Error("expected error when priv key exists")
	}
}

func TestCheckKeyPaths_NotExists(t *testing.T) {
	dir := t.TempDir()
	privPath := dir + "/new.pem"
	pubPath := dir + "/new.pub"

	err := checkKeyPaths(privPath, pubPath)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
