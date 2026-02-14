// Package main is the entry point for the agent-replay CLI.
// A standalone tool for forensic analysis of agent session logs.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	verbosity := 0 // 0=normal, 1=-v, 2=-vv
	noInteractive := false
	liveMode := false
	var costSpecs []string
	var paths []string

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
		case args[i] == "-f" || args[i] == "--follow" || args[i] == "--live":
			liveMode = true
		case args[i] == "--cost":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "error: --cost requires a value (model:input,output)\n")
				os.Exit(1)
			}
			i++
			costSpecs = append(costSpecs, args[i])
		case strings.HasPrefix(args[i], "--cost="):
			costSpecs = append(costSpecs, strings.TrimPrefix(args[i], "--cost="))
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

	// Parse cost specs into options
	opts, err := parseCostSpecs(costSpecs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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

		r := replay.New(os.Stdout, verbosity, opts...)
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
	r := replay.NewMulti(os.Stdout, verbosity, opts...)

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

// parseCostSpecs parses cost specifications into replay options.
func parseCostSpecs(specs []string) ([]replay.ReplayerOption, error) {
	var opts []replay.ReplayerOption
	for _, spec := range specs {
		model, inPrice, outPrice, err := parseCostSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid --cost %q: %w", spec, err)
		}
		opts = append(opts, replay.WithModelPricing(model, inPrice, outPrice))
	}
	return opts, nil
}

// parseCostSpec parses "model:input,output" format.
func parseCostSpec(spec string) (string, float64, float64, error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		return "", 0, 0, fmt.Errorf("expected model:input,output format")
	}
	model := parts[0]
	if model == "" {
		return "", 0, 0, fmt.Errorf("model name cannot be empty")
	}

	prices := strings.Split(parts[1], ",")
	if len(prices) != 2 {
		return "", 0, 0, fmt.Errorf("expected input,output prices")
	}

	inPrice, err := strconv.ParseFloat(strings.TrimSpace(prices[0]), 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid input price: %w", err)
	}
	outPrice, err := strconv.ParseFloat(strings.TrimSpace(prices[1]), 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid output price: %w", err)
	}

	return model, inPrice, outPrice, nil
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
  -v, --verbose     Show message content and tool results
  -vv               Very verbose - show full LLM prompts, responses, tokens, thinking
  --cost MODEL:IN,OUT  Model pricing (per 1M tokens). Repeatable.
                       Example: --cost claude-3-5-sonnet:3,15 --cost gpt-4o-mini:0.15,0.6
  --no-pager        Disable interactive pager (for piping)
  --version         Show version
  -h, --help        Show this help

Examples:
  agent-replay session.json
  agent-replay -v session1.json session2.json
  agent-replay -vv session.json          # Full LLM details
  agent-replay ./sessions/              # All .json files in directory
  agent-replay --no-pager session.json | grep SECURITY
  agent-replay -f session.json          # Watch for live updates
  agent-replay --cost claude-3-5-sonnet:3,15 session.json
  agent-replay --cost=claude-3-5-sonnet:3,15 --cost=gpt-4o-mini:0.15,0.6 session.json

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
