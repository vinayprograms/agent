package main

import (
	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/setup"
)

// newSetupCmd launches the interactive setup wizard. It targets a directory
// the same way `agent config` does, and pre-fills from an agent.toml already
// there. Credentials are always written to ~/.config/agent/credentials.toml,
// whatever the target: a key belongs to the user, not to a project.
func newSetupCmd(d deps) *cobra.Command {
	var tf targetFlags
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup wizard",
		Long: "Walk through the configuration questions and write agent.toml and\n" +
			"policy.toml to the target directory, pre-filled from an agent.toml\n" +
			"already there. The API key, if any, is always written to\n" +
			"~/.config/agent/credentials.toml.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := tf.resolve(d)
			if err != nil {
				return err
			}
			return setup.Run(cmd.Context(), t.dir)
		},
	}
	tf.bind(cmd)
	return cmd
}
