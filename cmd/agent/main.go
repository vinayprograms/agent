// Package main is the headless agent CLI: parse CLI, load what the command
// needs, dispatch. main is the only place that exits the process.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Build-time variables (set via ldflags)
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd(newDeps()).ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		code := 1
		if ec, ok := err.(interface{ ExitCode() int }); ok {
			code = ec.ExitCode()
		}
		os.Exit(code)
	}
}
