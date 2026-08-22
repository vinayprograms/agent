// Package replaycmd provides the shared "replay" cobra command used both as
// the `agent replay` subcommand and as the root of the standalone
// agent-replay binary.
package replaycmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/replay"
)

// Config customises the command for the binary that mounts it.
type Config struct {
	// Use is the command's name ("replay" as a subcommand, "agent-replay"
	// as a standalone root). Defaults to "replay".
	Use string
	// Version, Commit, BuildTime are reported by --version.
	Version, Commit, BuildTime string
}

// New builds the replay command.
func New(cfg Config) *cobra.Command {
	use := cfg.Use
	if use == "" {
		use = "replay"
	}

	var (
		verbose     int
		noPager     bool
		follow      bool
		live        bool
		costSpecs   []string
		showVersion bool
	)

	cmd := &cobra.Command{
		Use:           use + " [options] <session.jsonl>... | <directory>...",
		Short:         "Replay session logs for forensic analysis",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				return nil
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Fprintf(cmd.OutOrStdout(), "%s version %s (commit: %s, built: %s)\n",
					use, valueOr(cfg.Version, "dev"), valueOr(cfg.Commit, "unknown"), valueOr(cfg.BuildTime, "unknown"))
				return nil
			}
			return run(cmd, args, verbose, noPager, follow || live, costSpecs)
		},
	}

	cmd.Flags().CountVarP(&verbose, "verbose", "v", "Verbosity level (-v, -vv)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Live mode - watch a single file for changes and reload")
	cmd.Flags().BoolVar(&live, "live", false, "Alias for --follow")
	cmd.Flags().BoolVar(&noPager, "no-pager", false, "Disable interactive pager (for piping)")
	cmd.Flags().StringArrayVar(&costSpecs, "cost", nil, "Model pricing: model:input,output (per 1M tokens). Repeatable.")
	cmd.Flags().BoolVar(&showVersion, "version", false, "Show version information")

	return cmd
}

func valueOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// run performs the replay after flags have been parsed.
func run(cmd *cobra.Command, paths []string, verbosity int, noPager, liveMode bool, costSpecs []string) error {
	opts, err := parsePricingOpts(costSpecs)
	if err != nil {
		return err
	}

	if liveMode {
		if len(paths) != 1 {
			return fmt.Errorf("--follow only works with a single session file")
		}
		info, err := os.Stat(paths[0])
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("--follow requires a file, not a directory")
		}
		r := replay.New(verbosity, opts...)
		return r.ReplayFileLive(paths[0])
	}

	sessionFiles, err := expandPaths(paths)
	if err != nil {
		return err
	}
	if len(sessionFiles) == 0 {
		return fmt.Errorf("no session files found")
	}

	r := replay.NewMulti(verbosity, opts...)

	out := cmd.OutOrStdout()
	if !noPager && isTerminal(out) {
		return r.ReplayFilesInteractive(sessionFiles)
	}
	return r.ReplayFiles(out, sessionFiles)
}

func parsePricingOpts(specs []string) ([]replay.ReplayerOption, error) {
	var opts []replay.ReplayerOption
	for _, spec := range specs {
		model, inPrice, outPrice, err := replay.ParsePricing(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid --cost %q: %w", spec, err)
		}
		opts = append(opts, replay.Pricing(model, inPrice, outPrice))
	}
	return opts, nil
}

// expandPaths takes file paths and directories and returns all session log
// files. Directories are globbed for *.jsonl (the current session format)
// as well as legacy *.json files.
func expandPaths(paths []string) ([]string, error) {
	var files []string

	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("cannot access %s: %w", p, err)
		}

		if !info.IsDir() {
			files = append(files, p)
			continue
		}

		var dirFiles []string
		seen := make(map[string]bool)
		for _, pattern := range []string{"*.jsonl", "*.json"} {
			matches, err := filepath.Glob(filepath.Join(p, pattern))
			if err != nil {
				return nil, fmt.Errorf("cannot glob directory %s: %w", p, err)
			}
			for _, m := range matches {
				if !seen[m] {
					seen[m] = true
					dirFiles = append(dirFiles, m)
				}
			}
		}
		sort.Strings(dirFiles)
		files = append(files, dirFiles...)
	}

	return files, nil
}

// isTerminal reports whether w is an interactive terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
