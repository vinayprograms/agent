package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

func TestVerifyCmd_NoKey(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"verify", "package.agent"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Verify.Package != "package.agent" {
		t.Errorf("expected package 'package.agent', got %q", cli.Verify.Package)
	}
	if cli.Verify.Key != "" {
		t.Errorf("expected empty key, got %q", cli.Verify.Key)
	}
}

func TestVerifyCmd_WithKey(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"verify", "package.agent", "--key", "key.pub"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Verify.Key != "key.pub" {
		t.Errorf("expected key 'key.pub', got %q", cli.Verify.Key)
	}
}
