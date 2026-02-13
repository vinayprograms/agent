package main

import (
	"fmt"
	"os"

	"github.com/vinayprograms/agent/internal/setup"
)

// runSetup launches the interactive setup wizard.
func runSetup() {
	if err := setup.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
