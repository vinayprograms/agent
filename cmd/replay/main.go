// Package main is the entry point for the agent-replay CLI.
// A standalone tool for forensic analysis of agent session logs; it mounts
// the shared replay command (internal/replaycmd) as its root.
package main

import (
	"fmt"
	"os"

	"github.com/vinayprograms/agent/internal/replaycmd"
)

// Build-time variables (set via ldflags).
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	root := replaycmd.New(replaycmd.Config{Use: "agent-replay", Version: version, Commit: commit, BuildTime: buildTime})
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
