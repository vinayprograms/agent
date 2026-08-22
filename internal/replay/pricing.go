package replay

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsePricing parses a "model:input,output" cost spec (prices per 1M
// tokens) as accepted by the replay CLIs' --cost flag.
func ParsePricing(spec string) (model string, inputPer1M, outputPer1M float64, err error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		return "", 0, 0, fmt.Errorf("expected model:input,output format")
	}
	model = parts[0]
	if model == "" {
		return "", 0, 0, fmt.Errorf("model name cannot be empty")
	}

	prices := strings.Split(parts[1], ",")
	if len(prices) != 2 {
		return "", 0, 0, fmt.Errorf("expected input,output prices")
	}

	inputPer1M, err = strconv.ParseFloat(strings.TrimSpace(prices[0]), 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid input price: %w", err)
	}
	outputPer1M, err = strconv.ParseFloat(strings.TrimSpace(prices[1]), 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid output price: %w", err)
	}

	return model, inputPer1M, outputPer1M, nil
}
