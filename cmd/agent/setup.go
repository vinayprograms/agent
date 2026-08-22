package main

import (
	"context"
	"fmt"
	"os"

	"github.com/vinayprograms/agent/internal/setup"
)

// runSetup launches the interactive setup wizard. Matches the
// context.Background() convention used by the other top-level commands
// (see RunCmd.Run in main.go) since neither SetupCmd.Run nor cobra's RunE
// here currently thread a caller context down to this call.
func runSetup() {
	if err := setup.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
