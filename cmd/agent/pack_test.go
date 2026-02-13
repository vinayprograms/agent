package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

func TestPackCmd_AllFlags(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{
		"pack", "mydir",
		"--output", "output.agent",
		"--sign", "key.pem",
		"--author", "Test Author",
		"--email", "test@example.com",
		"--license", "MIT",
	})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Pack.Dir != "mydir" {
		t.Errorf("expected dir 'mydir', got %q", cli.Pack.Dir)
	}
	if cli.Pack.Output != "output.agent" {
		t.Errorf("expected output 'output.agent', got %q", cli.Pack.Output)
	}
	if cli.Pack.Sign != "key.pem" {
		t.Errorf("expected sign 'key.pem', got %q", cli.Pack.Sign)
	}
	if cli.Pack.Author != "Test Author" {
		t.Errorf("expected author 'Test Author', got %q", cli.Pack.Author)
	}
	if cli.Pack.Email != "test@example.com" {
		t.Errorf("expected email, got %q", cli.Pack.Email)
	}
	if cli.Pack.License != "MIT" {
		t.Errorf("expected license 'MIT', got %q", cli.Pack.License)
	}
}

func TestPackCmd_ShortOutput(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"pack", "src", "-o", "out.zip"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Pack.Output != "out.zip" {
		t.Errorf("expected output 'out.zip', got %q", cli.Pack.Output)
	}
}
