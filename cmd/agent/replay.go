package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vinayprograms/agent/internal/replay"
)

// replaySession replays a session from a JSON file for forensic analysis.
func replaySession(args []string) {
	verbosity := 0 // 0=normal, 1=-v, 2=-vv
	noInteractive := false
	var sessionPath string

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-vv":
			verbosity = 2
		case args[i] == "-v" || args[i] == "--verbose":
			if verbosity < 1 {
				verbosity = 1
			}
		case args[i] == "--no-pager":
			noInteractive = true
		case !strings.HasPrefix(args[i], "-"):
			sessionPath = args[i]
		}
	}

	if sessionPath == "" {
		printReplayUsage()
		os.Exit(1)
	}

	r := replay.New(os.Stdout, verbosity)

	// Use interactive pager when stdout is a TTY and not disabled
	if !noInteractive && isTerminal(os.Stdout) {
		if err := r.ReplayFileInteractive(sessionPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := r.ReplayFile(sessionPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}

func printReplayUsage() {
	fmt.Fprintf(os.Stderr, "Usage: agent replay [-v|--verbose|-vv] [--no-pager] <session.json>\n")
	fmt.Fprintf(os.Stderr, "\nReplays a session for forensic analysis.\n")
	fmt.Fprintf(os.Stderr, "\nOptions:\n")
	fmt.Fprintf(os.Stderr, "  -v, --verbose    Show message content and tool results\n")
	fmt.Fprintf(os.Stderr, "  -vv              Very verbose - show full LLM prompts, responses, tokens\n")
	fmt.Fprintf(os.Stderr, "  --no-pager       Disable interactive pager (for piping)\n")
}
