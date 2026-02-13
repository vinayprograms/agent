package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

func TestReplayCmd_Basic(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"replay", "session.json"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Replay.Session != "session.json" {
		t.Errorf("expected session 'session.json', got %q", cli.Replay.Session)
	}
	if cli.Replay.Verbose != 0 {
		t.Errorf("expected verbose=0, got %d", cli.Replay.Verbose)
	}
}

func TestReplayCmd_Verbose(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"replay", "-v", "session.json"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Replay.Verbose != 1 {
		t.Errorf("expected verbose=1, got %d", cli.Replay.Verbose)
	}
}

func TestReplayCmd_VeryVerbose(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"replay", "-vv", "session.json"})
	if err != nil {
		t.Fatal(err)
	}

	if cli.Replay.Verbose != 2 {
		t.Errorf("expected verbose=2, got %d", cli.Replay.Verbose)
	}
}

func TestReplayCmd_NoPager(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli)
	if err != nil {
		t.Fatal(err)
	}

	_, err = parser.Parse([]string{"replay", "--no-pager", "session.json"})
	if err != nil {
		t.Fatal(err)
	}

	if !cli.Replay.NoPager {
		t.Error("expected no-pager to be true")
	}
}
