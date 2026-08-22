package main

import (
	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/setup"
)

// newSetupCmd launches the interactive setup wizard.
func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup wizard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return setup.Run(cmd.Context(), "")
		},
	}
}
