// Package replaycmd provides the shared "replay" cobra command used both as
// the `agent replay` subcommand and as the root of the standalone
// agent-replay binary.
package replaycmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agent/internal/replay"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/term"
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
		list        bool
		stateDir    string
		configPath  string
		sel         filter
	)

	cmd := &cobra.Command{
		Use:   use + " [options] [id | prefix | file | directory]...",
		Short: "List and replay session logs for forensic analysis",
		Long: `List and replay session logs for forensic analysis.

With no arguments, lists the recorded sessions of the state directory.
Arguments select what to replay: a session id, a unique id prefix, a
session .jsonl file, or a directory to glob for session files. The
selectors below filter recorded sessions the same way; --list prints the
matching sessions as a table instead of replaying them.

Navigation keys in the interactive pager:
  j / k    scroll down / up one line
  g / G    jump to top / bottom
  f        toggle follow mode
  q        quit

Examples:
  ` + use + `                          list every recorded session
  ` + use + ` --last                   replay the most recent one
  ` + use + ` a1b2c3d4                 replay the session with that id prefix
  ` + use + ` --name hello --list      list the sessions of one workflow
  ` + use + ` --cost gpt-4o:5,15 session.jsonl`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Fprintf(cmd.OutOrStdout(), "%s version %s (commit: %s, built: %s)\n",
					use, valueOr(cfg.Version, "dev"), valueOr(cfg.Commit, "unknown"), valueOr(cfg.BuildTime, "unknown"))
				return nil
			}
			files, err := selectFiles(cmd, args, sel, list, configPath, stateDir)
			if err != nil || files == nil {
				return err
			}
			return run(cmd, files, verbose, noPager, follow || live, costSpecs)
		},
	}

	cmd.Flags().CountVarP(&verbose, "verbose", "v", "Verbosity level (-v, -vv)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Live mode - watch a single file for changes and reload")
	cmd.Flags().BoolVar(&live, "live", false, "Alias for --follow")
	cmd.Flags().BoolVar(&noPager, "no-pager", false, "Disable interactive pager (for piping)")
	cmd.Flags().StringArrayVar(&costSpecs, "cost", nil, "Model pricing: model:input,output (per 1M tokens). Repeatable.")
	cmd.Flags().BoolVar(&showVersion, "version", false, "Show version information")
	cmd.Flags().BoolVar(&list, "list", false, "List matching sessions as a table instead of replaying them")
	cmd.Flags().BoolVar(&sel.last, "last", false, "Select the most recently created session")
	cmd.Flags().StringVar(&sel.name, "name", "", "Select sessions by workflow NAME")
	cmd.Flags().StringVar(&sel.agentfile, "agentfile", "", "Select sessions by Agentfile path or file name")
	cmd.Flags().StringVar(&sel.label, "label", "", "Select sessions by deployment label")
	cmd.Flags().StringVar(&sel.status, "status", "", "Select sessions by status (running, complete, failed, aborted)")
	cmd.Flags().DurationVar(&sel.since, "since", 0, "Select sessions created within this duration (e.g. 2h)")
	cmd.Flags().StringVar(&stateDir, "state", "", "Override state location (default: [state] location from config)")
	cmd.Flags().StringVar(&configPath, "config", "", "Config file path")

	return cmd
}

func valueOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// selectFiles turns the positional arguments and selectors into the
// session files to replay. It returns nil files when it has already done
// the work — printing the session table.
func selectFiles(cmd *cobra.Command, args []string, sel filter, list bool, configPath, stateOverride string) ([]string, error) {
	// Plain file and directory arguments replay without a state directory.
	if len(args) > 0 && !sel.active() && !list && allExist(args) {
		files, err := expandPaths(args)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, errors.New("no session files found")
		}
		return files, nil
	}

	dir, err := config.StateDir(configPath, stateOverride, "")
	if err != nil {
		return nil, err
	}
	st := openStore(dir, cmd.ErrOrStderr())

	var matched []session.Summary
	if len(args) > 0 {
		for _, arg := range args {
			resolved, err := st.resolve(arg)
			if err != nil {
				return nil, err
			}
			matched = append(matched, resolved...)
		}
		matched = sel.apply(matched)
	} else {
		if st.empty() {
			return nil, fmt.Errorf("no sessions recorded yet in %s — run an agent first, or pass a session file", st.dir)
		}
		matched = sel.apply(st.sessions)
	}
	if len(matched) == 0 {
		return nil, errors.New("no sessions match those selectors")
	}
	// --list forces the table; so does a bare invocation with nothing to
	// single out a session.
	if list || (len(args) == 0 && !sel.active()) {
		writeTable(cmd.OutOrStdout(), matched)
		return nil, nil
	}
	return paths(matched), nil
}

// allExist reports whether every argument names an existing path.
func allExist(args []string) bool {
	for _, a := range args {
		if _, err := os.Stat(a); err != nil {
			return false
		}
	}
	return true
}

// run performs the replay after flags have been parsed.
func run(cmd *cobra.Command, paths []string, verbosity int, noPager, liveMode bool, costSpecs []string) error {
	opts, err := parsePricingOpts(costSpecs)
	if err != nil {
		return err
	}

	if liveMode {
		if len(paths) != 1 {
			return errors.New("--follow only works with a single session file")
		}
		info, err := os.Stat(paths[0])
		if err != nil {
			return err
		}
		if info.IsDir() {
			return errors.New("--follow requires a file, not a directory")
		}
		r := replay.New(verbosity, opts...)
		return r.ReplayFileLive(paths[0])
	}

	sessionFiles, err := expandPaths(paths)
	if err != nil {
		return err
	}
	if len(sessionFiles) == 0 {
		return errors.New("no session files found")
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
func isTerminal(w io.Writer) bool { return term.IsTerminal(w) }
