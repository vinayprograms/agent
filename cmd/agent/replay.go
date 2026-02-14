package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/vinayprograms/agent/internal/replay"
)

// runReplay replays a session from a JSON file for forensic analysis.
func runReplay(sessionPath string, verbosity int, noPager bool, costSpecs []string) error {
	opts := []replay.ReplayerOption{}

	// Parse cost specs: model:input,output (per 1M tokens)
	for _, spec := range costSpecs {
		model, inPrice, outPrice, err := parseCostSpec(spec)
		if err != nil {
			return fmt.Errorf("invalid --cost spec %q: %w", spec, err)
		}
		opts = append(opts, replay.WithModelPricing(model, inPrice, outPrice))
	}

	r := replay.New(os.Stdout, verbosity, opts...)

	// Use interactive pager when stdout is a TTY and not disabled
	if !noPager && isTerminal(os.Stdout) {
		return r.ReplayFileInteractive(sessionPath)
	}
	return r.ReplayFile(sessionPath)
}

// parseCostSpec parses "model:input,output" format.
// Returns model name, input price per 1M, output price per 1M.
func parseCostSpec(spec string) (string, float64, float64, error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		return "", 0, 0, fmt.Errorf("expected model:input,output format")
	}
	model := parts[0]
	if model == "" {
		return "", 0, 0, fmt.Errorf("model name cannot be empty")
	}

	prices := strings.Split(parts[1], ",")
	if len(prices) != 2 {
		return "", 0, 0, fmt.Errorf("expected input,output prices")
	}

	inPrice, err := strconv.ParseFloat(strings.TrimSpace(prices[0]), 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid input price: %w", err)
	}
	outPrice, err := strconv.ParseFloat(strings.TrimSpace(prices[1]), 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid output price: %w", err)
	}

	return model, inPrice, outPrice, nil
}
