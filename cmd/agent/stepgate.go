package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// stepGate returns an executor step gate that asks on out — the command's
// stderr — and reads the answer from in. Enter or "y" continues, "n" aborts,
// anything else re-asks; end of input aborts.
func stepGate(in io.Reader, out io.Writer) func(context.Context, string, string) (bool, error) {
	r := bufio.NewReader(in)
	return func(_ context.Context, step, goal string) (bool, error) {
		for {
			fmt.Fprintf(out, "RUN %s › goal %q finished. Continue? [Y/n] ", step, goal)
			line, err := r.ReadString('\n')
			if line == "" && err != nil {
				fmt.Fprintln(out)
				return false, nil
			}
			switch strings.ToLower(strings.TrimSpace(line)) {
			case "", "y", "yes":
				return true, nil
			case "n", "no":
				return false, nil
			}
		}
	}
}
