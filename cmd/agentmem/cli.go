package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// requireStoragePath is a cobra.Args validator for the single-argument
// subcommands (list, stats, graph, scratchpad), giving the same
// "storage path required" message the hand-rolled CLI used instead of
// cobra's generic "accepts N arg(s)" wording.
func requireStoragePath(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("storage path required")
	}
	return nil
}

// requireSearchArgs validates `search <query> <storage-path>`.
func requireSearchArgs(cmd *cobra.Command, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("query and storage path required\nUsage: agentmem search <query> <storage-path>")
	}
	return nil
}

const usageText = `agentmem - Memory investigation tool for headless-agent

Usage:
  agentmem <command> [options] <storage-path>

Commands:
  list       List all stored observations
  search     Search observations by query
  stats      Show memory statistics
  graph      Inspect semantic graph
  scratchpad Dump scratchpad (key-value store)

Examples:
  agentmem list ./storage
  agentmem list --category=finding --limit=10 ./storage
  agentmem search "database choice" ./storage
  agentmem stats ./storage
  agentmem graph --term=api ./storage
  agentmem scratchpad ./storage`

// NewRootCmd builds the agentmem cobra root command with its subcommands:
// list, search, stats, graph, scratchpad.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "agentmem",
		Short:         "Memory investigation tool for headless-agent",
		Long:          usageText,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), usageText)
				return errNoCommand
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "Unknown command:", args[0])
			fmt.Fprintln(cmd.OutOrStdout(), usageText)
			return fmt.Errorf("unknown command: %s", args[0])
		},
	}
	root.SetHelpTemplate(usageText + "\n")

	root.AddCommand(
		newListCmd(),
		newSearchCmd(),
		newStatsCmd(),
		newGraphCmd(),
		newScratchpadCmd(),
	)

	return root
}

// errNoCommand signals "no subcommand given" without printing an extra
// "Error: ..." line (main only cares about the exit code here; the usage
// text was already written to stdout).
var errNoCommand = fmt.Errorf("no command given")

func newListCmd() *cobra.Command {
	var category string
	var limit int
	cmd := &cobra.Command{
		Use:   "list <storage-path>",
		Short: "List all stored observations",
		Args:  requireStoragePath,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdList(cmd, args[len(args)-1], category, limit)
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "Filter by category (finding|insight|lesson)")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum observations to list")
	return cmd
}

func newSearchCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query> <storage-path>",
		Short: "Search observations by query",
		Args:  requireSearchArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdSearch(cmd, args[0], args[len(args)-1], limit)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum results per category")
	return cmd
}

func newStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats <storage-path>",
		Short: "Show memory statistics",
		Args:  requireStoragePath,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdStats(cmd, args[len(args)-1])
		},
	}
	return cmd
}

func newGraphCmd() *cobra.Command {
	var term string
	cmd := &cobra.Command{
		Use:   "graph <storage-path>",
		Short: "Inspect semantic graph",
		Args:  requireStoragePath,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdGraph(cmd, args[len(args)-1], term)
		},
	}
	cmd.Flags().StringVar(&term, "term", "", "Show a specific term's related terms")
	return cmd
}

func newScratchpadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scratchpad <storage-path>",
		Short: "Dump scratchpad (key-value store)",
		Args:  requireStoragePath,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdScratchpad(cmd, args[len(args)-1])
		},
	}
	return cmd
}
