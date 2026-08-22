// agentmem - Memory investigation tool for headless-agent
//
// Commands:
//
//	agentmem list [--category=finding|insight|lesson] [--limit=N] <storage-path>
//	agentmem search <query> [--limit=N] <storage-path>
//	agentmem stats <storage-path>
//	agentmem graph [--term=X] <storage-path>
//	agentmem scratchpad <storage-path>
package main

import (
	"fmt"
	"os"
)

func main() {
	root := NewRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
