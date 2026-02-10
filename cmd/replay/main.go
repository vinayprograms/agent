// Package main is the entry point for the agent-replay CLI.
// A standalone tool for forensic analysis of agent session logs.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinayprograms/agent/internal/replay"
)

// Build-time variables
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	args := os.Args[1:]

	// Parse flags
	verbose := false
	noInteractive := false
	liveMode := false
	var paths []string

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-v" || args[i] == "--verbose":
			verbose = true
		case args[i] == "--no-pager":
			noInteractive = true
		case args[i] == "-f" || args[i] == "--follow" || args[i] == "--live":
			liveMode = true
		case args[i] == "-h" || args[i] == "--help":
			printUsage()
			os.Exit(0)
		case args[i] == "--version":
			fmt.Printf("agent-replay version %s (commit: %s, built: %s)\n", version, commit, buildTime)
			os.Exit(0)
		case !strings.HasPrefix(args[i], "-"):
			paths = append(paths, args[i])
		default:
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	if len(paths) == 0 {
		printUsage()
		os.Exit(1)
	}

	// Live mode only works with a single file
	if liveMode {
		if len(paths) != 1 {
			fmt.Fprintf(os.Stderr, "error: --follow only works with a single session file\n")
			os.Exit(1)
		}
		// Check it's a file, not a directory
		info, err := os.Stat(paths[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if info.IsDir() {
			fmt.Fprintf(os.Stderr, "error: --follow requires a file, not a directory\n")
			os.Exit(1)
		}

		r := replay.New(os.Stdout, verbose)
		if err := r.ReplayFileLive(paths[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Expand directories to session files
	sessionFiles, err := expandPaths(paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(sessionFiles) == 0 {
		fmt.Fprintf(os.Stderr, "error: no session files found\n")
		os.Exit(1)
	}

	// Create multi-session replayer
	r := replay.NewMulti(os.Stdout, verbose)

	// Use interactive pager when stdout is a TTY and not disabled
	if !noInteractive && isTerminal(os.Stdout) {
		if err := r.ReplayFilesInteractive(sessionFiles); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := r.ReplayFiles(sessionFiles); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}

func printUsage() {
	fmt.Println(`agent-replay - Forensic analysis tool for agent session logs

Usage:
  agent-replay [options] <session.json>...
  agent-replay [options] <directory>
  agent-replay -f <session.json>        # Live mode

Arguments:
  <session.json>    One or more session log files
  <directory>       Directory containing session logs (*.json)

Options:
  -f, --follow      Live mode - watch file for changes and reload
  -v, --verbose     Show full message and result content
  --no-pager        Disable interactive pager (for piping)
  --version         Show version
  -h, --help        Show this help

Examples:
  agent-replay session.json
  agent-replay -v session1.json session2.json
  agent-replay ./sessions/              # All .json files in directory
  agent-replay --no-pager session.json | grep SECURITY
  agent-replay -f session.json          # Watch for live updates

Navigation (interactive mode):
  ↑/↓, j/k          Scroll line by line
  PgUp/PgDn         Scroll by page
  g/G               Jump to top/bottom
  f                 Follow (jump to bottom, useful in live mode)
  q, Esc            Quit`)
}

// expandPaths takes file paths and directories and returns all session JSON files.
func expandPaths(paths []string) ([]string, error) {
	var files []string

	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("cannot access %s: %w", p, err)
		}

		if info.IsDir() {
			// Find all .json files in directory
			entries, err := os.ReadDir(p)
			if err != nil {
				return nil, fmt.Errorf("cannot read directory %s: %w", p, err)
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
					files = append(files, filepath.Join(p, entry.Name()))
				}
			}
		} else {
			files = append(files, p)
		}
	}

	return files, nil
}

// isTerminal checks if the given file is a terminal.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
