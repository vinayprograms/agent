package main

import (
	"os"

	"github.com/vinayprograms/agent/internal/replay"
)

// runReplay replays a session from a JSON file for forensic analysis.
func runReplay(sessionPath string, verbosity int, noPager bool) error {
	r := replay.New(os.Stdout, verbosity)

	// Use interactive pager when stdout is a TTY and not disabled
	if !noPager && isTerminal(os.Stdout) {
		return r.ReplayFileInteractive(sessionPath)
	}
	return r.ReplayFile(sessionPath)
}
