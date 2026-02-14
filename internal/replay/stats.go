package replay

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinayprograms/agent/internal/session"
)

// ModelUsage tracks token usage for a specific model.
type ModelUsage struct {
	Calls     int
	TokensIn  int64
	TokensOut int64
}

// Stats holds aggregate statistics for a session.
type Stats struct {
	// Total workflow duration
	TotalDurationMs int64

	// Per-goal durations
	GoalDurations map[string]int64

	// Token usage by model
	ModelUsage map[string]*ModelUsage

	// LLM response times
	LLMCallCount    int
	LLMTotalMs      int64
	LLMAvgMs        int64

	// Security supervisor (execution)
	ExecSupervisorCount   int
	ExecSupervisorTotalMs int64
	ExecSupervisorAvgMs   int64

	// Security triage
	SecurityTriageCount   int
	SecurityTriageTotalMs int64
	SecurityTriageAvgMs   int64

	// Security supervisor
	SecuritySupervisorCount   int
	SecuritySupervisorTotalMs int64
	SecuritySupervisorAvgMs   int64

	// Bash security (deterministic)
	BashDeterministicCount int
	// Bash security (LLM)
	BashLLMCount   int
	BashLLMTotalMs int64
	BashLLMAvgMs   int64
}

// ComputeStats calculates aggregate statistics from session events.
func ComputeStats(sess *session.Session) *Stats {
	stats := &Stats{
		GoalDurations: make(map[string]int64),
		ModelUsage:    make(map[string]*ModelUsage),
	}

	var firstEvent, lastEvent time.Time

	for _, event := range sess.Events {
		// Track overall duration
		if firstEvent.IsZero() || event.Timestamp.Before(firstEvent) {
			firstEvent = event.Timestamp
		}
		if lastEvent.IsZero() || event.Timestamp.After(lastEvent) {
			lastEvent = event.Timestamp
		}

		switch event.Type {
		case session.EventGoalEnd:
			if event.DurationMs > 0 {
				stats.GoalDurations[event.Goal] = event.DurationMs
			}

		case session.EventAssistant:
			// LLM response - track latency and tokens
			if event.Meta != nil {
				if event.Meta.LatencyMs > 0 {
					stats.LLMCallCount++
					stats.LLMTotalMs += event.Meta.LatencyMs
				}
				// Track token usage by model
				if event.Meta.Model != "" {
					stats.addModelUsage(event.Meta.Model, event.Meta.TokensIn, event.Meta.TokensOut)
				}
			}

		case session.EventPhaseSupervise:
			// Execution supervisor
			if event.DurationMs > 0 {
				stats.ExecSupervisorCount++
				stats.ExecSupervisorTotalMs += event.DurationMs
			}
			// Track token usage
			if event.Meta != nil && event.Meta.Model != "" {
				stats.addModelUsage(event.Meta.Model, event.Meta.TokensIn, event.Meta.TokensOut)
			}

		case session.EventSecurityTriage:
			// Use DurationMs first (direct on event), fallback to Meta.LatencyMs
			var latency int64
			if event.DurationMs > 0 {
				latency = event.DurationMs
			} else if event.Meta != nil && event.Meta.LatencyMs > 0 {
				latency = event.Meta.LatencyMs
			}
			if latency > 0 {
				stats.SecurityTriageCount++
				stats.SecurityTriageTotalMs += latency
			}
			// Track token usage
			if event.Meta != nil && event.Meta.Model != "" {
				stats.addModelUsage(event.Meta.Model, event.Meta.TokensIn, event.Meta.TokensOut)
			}

		case session.EventSecuritySupervisor:
			var latency int64
			if event.DurationMs > 0 {
				latency = event.DurationMs
			} else if event.Meta != nil && event.Meta.LatencyMs > 0 {
				latency = event.Meta.LatencyMs
			}
			if latency > 0 {
				stats.SecuritySupervisorCount++
				stats.SecuritySupervisorTotalMs += latency
			}
			// Track token usage
			if event.Meta != nil && event.Meta.Model != "" {
				stats.addModelUsage(event.Meta.Model, event.Meta.TokensIn, event.Meta.TokensOut)
			}

		case session.EventBashSecurity:
			// Use structured Meta when available
			if event.Meta != nil {
				step := event.Meta.CheckName
				if step == "deterministic" {
					stats.BashDeterministicCount++
				} else if step == "llm" {
					stats.BashLLMCount++
					if event.DurationMs > 0 {
						stats.BashLLMTotalMs += event.DurationMs
					} else if event.Meta.LatencyMs > 0 {
						stats.BashLLMTotalMs += event.Meta.LatencyMs
					}
					// Track token usage for bash LLM
					if event.Meta.Model != "" {
						stats.addModelUsage(event.Meta.Model, event.Meta.TokensIn, event.Meta.TokensOut)
					}
				}
			} else {
				// Fallback: Parse step from content: "[deterministic] ..." or "[llm] ..."
				if len(event.Content) > 1 && event.Content[0] == '[' {
					if len(event.Content) > 14 && event.Content[1:14] == "deterministic" {
						stats.BashDeterministicCount++
					} else if len(event.Content) > 4 && event.Content[1:4] == "llm" {
						stats.BashLLMCount++
						if event.DurationMs > 0 {
							stats.BashLLMTotalMs += event.DurationMs
						}
					}
				}
			}
		}
	}

	// Calculate total duration
	if !firstEvent.IsZero() && !lastEvent.IsZero() {
		stats.TotalDurationMs = lastEvent.Sub(firstEvent).Milliseconds()
	}

	// Calculate averages
	if stats.LLMCallCount > 0 {
		stats.LLMAvgMs = stats.LLMTotalMs / int64(stats.LLMCallCount)
	}
	if stats.ExecSupervisorCount > 0 {
		stats.ExecSupervisorAvgMs = stats.ExecSupervisorTotalMs / int64(stats.ExecSupervisorCount)
	}
	if stats.SecurityTriageCount > 0 {
		stats.SecurityTriageAvgMs = stats.SecurityTriageTotalMs / int64(stats.SecurityTriageCount)
	}
	if stats.SecuritySupervisorCount > 0 {
		stats.SecuritySupervisorAvgMs = stats.SecuritySupervisorTotalMs / int64(stats.SecuritySupervisorCount)
	}
	if stats.BashLLMCount > 0 {
		stats.BashLLMAvgMs = stats.BashLLMTotalMs / int64(stats.BashLLMCount)
	}

	return stats
}

// addModelUsage adds token usage for a model.
func (s *Stats) addModelUsage(model string, tokensIn, tokensOut int) {
	if model == "" {
		return
	}
	usage, ok := s.ModelUsage[model]
	if !ok {
		usage = &ModelUsage{}
		s.ModelUsage[model] = usage
	}
	usage.Calls++
	usage.TokensIn += int64(tokensIn)
	usage.TokensOut += int64(tokensOut)
}

// PrintStats outputs the statistics to the writer.
func PrintStats(w io.Writer, stats *Stats) {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	
	fmt.Fprintln(w)
	fmt.Fprintln(w, headerStyle.Render("═══════════════════════════════════════════════════════════════════"))
	fmt.Fprintln(w, headerStyle.Render("                         SESSION STATISTICS                         "))
	fmt.Fprintln(w, headerStyle.Render("═══════════════════════════════════════════════════════════════════"))
	fmt.Fprintln(w)

	// Total duration
	fmt.Fprintf(w, "%s %s\n",
		labelStyle.Render("Total Duration:"),
		valueStyle.Render(formatDuration(stats.TotalDurationMs)))
	fmt.Fprintln(w)

	// Goal durations
	if len(stats.GoalDurations) > 0 {
		fmt.Fprintln(w, headerStyle.Render("Goal Durations:"))
		// Sort goals by name
		var goals []string
		for g := range stats.GoalDurations {
			goals = append(goals, g)
		}
		sort.Strings(goals)
		for _, g := range goals {
			fmt.Fprintf(w, "  %s %s\n",
				labelStyle.Render(g+":"),
				valueStyle.Render(formatDuration(stats.GoalDurations[g])))
		}
		fmt.Fprintln(w)
	}

	// LLM response times
	if stats.LLMCallCount > 0 {
		fmt.Fprintln(w, headerStyle.Render("LLM Response Times:"))
		fmt.Fprintf(w, "  %s %s\n",
			labelStyle.Render("Calls:"),
			valueStyle.Render(fmt.Sprintf("%d", stats.LLMCallCount)))
		fmt.Fprintf(w, "  %s %s\n",
			labelStyle.Render("Total:"),
			valueStyle.Render(formatDuration(stats.LLMTotalMs)))
		fmt.Fprintf(w, "  %s %s\n",
			labelStyle.Render("Average:"),
			valueStyle.Render(formatDuration(stats.LLMAvgMs)))
		fmt.Fprintln(w)
	}

	// Execution supervisor
	if stats.ExecSupervisorCount > 0 {
		fmt.Fprintln(w, headerStyle.Render("Execution Supervisor:"))
		fmt.Fprintf(w, "  %s %s\n",
			labelStyle.Render("Invocations:"),
			valueStyle.Render(fmt.Sprintf("%d", stats.ExecSupervisorCount)))
		fmt.Fprintf(w, "  %s %s\n",
			labelStyle.Render("Total:"),
			valueStyle.Render(formatDuration(stats.ExecSupervisorTotalMs)))
		fmt.Fprintf(w, "  %s %s\n",
			labelStyle.Render("Average:"),
			valueStyle.Render(formatDuration(stats.ExecSupervisorAvgMs)))
		fmt.Fprintln(w)
	}

	// Security checks
	if stats.SecurityTriageCount > 0 || stats.SecuritySupervisorCount > 0 {
		fmt.Fprintln(w, headerStyle.Render("Security Checks:"))
		if stats.SecurityTriageCount > 0 {
			fmt.Fprintf(w, "  %s\n", labelStyle.Render("Triage (Tier 2):"))
			fmt.Fprintf(w, "    %s %s\n",
				labelStyle.Render("Count:"),
				valueStyle.Render(fmt.Sprintf("%d", stats.SecurityTriageCount)))
			fmt.Fprintf(w, "    %s %s\n",
				labelStyle.Render("Avg:"),
				valueStyle.Render(formatDuration(stats.SecurityTriageAvgMs)))
		}
		if stats.SecuritySupervisorCount > 0 {
			fmt.Fprintf(w, "  %s\n", labelStyle.Render("Supervisor (Tier 3):"))
			fmt.Fprintf(w, "    %s %s\n",
				labelStyle.Render("Count:"),
				valueStyle.Render(fmt.Sprintf("%d", stats.SecuritySupervisorCount)))
			fmt.Fprintf(w, "    %s %s\n",
				labelStyle.Render("Avg:"),
				valueStyle.Render(formatDuration(stats.SecuritySupervisorAvgMs)))
		}
		fmt.Fprintln(w)
	}

	// Bash security
	if stats.BashDeterministicCount > 0 || stats.BashLLMCount > 0 {
		fmt.Fprintln(w, headerStyle.Render("Bash Security:"))
		if stats.BashDeterministicCount > 0 {
			fmt.Fprintf(w, "  %s %s\n",
				labelStyle.Render("Deterministic:"),
				valueStyle.Render(fmt.Sprintf("%d checks", stats.BashDeterministicCount)))
		}
		if stats.BashLLMCount > 0 {
			fmt.Fprintf(w, "  %s %s %s\n",
				labelStyle.Render("LLM:"),
				valueStyle.Render(fmt.Sprintf("%d checks", stats.BashLLMCount)),
				labelStyle.Render(fmt.Sprintf("(avg %s)", formatDuration(stats.BashLLMAvgMs))))
		}
		fmt.Fprintln(w)
	}
}

// Pricing holds cost per 1M tokens.
type Pricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

// PrintTokenUsage outputs token usage by model with optional cost calculation.
func PrintTokenUsage(w io.Writer, stats *Stats, pricing *Pricing) {
	if len(stats.ModelUsage) == 0 {
		return
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	costStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // Green

	fmt.Fprintln(w, headerStyle.Render("Token Usage by Model:"))

	// Sort models for consistent output
	var models []string
	for m := range stats.ModelUsage {
		models = append(models, m)
	}
	sort.Strings(models)

	var totalIn, totalOut int64
	var totalCost float64

	for _, model := range models {
		usage := stats.ModelUsage[model]
		totalIn += usage.TokensIn
		totalOut += usage.TokensOut

		fmt.Fprintf(w, "  %s\n", valueStyle.Render(model))
		fmt.Fprintf(w, "    %s %s\n",
			labelStyle.Render("Calls:"),
			valueStyle.Render(fmt.Sprintf("%d", usage.Calls)))
		fmt.Fprintf(w, "    %s %s\n",
			labelStyle.Render("Input:"),
			valueStyle.Render(formatTokens(usage.TokensIn)))
		fmt.Fprintf(w, "    %s %s\n",
			labelStyle.Render("Output:"),
			valueStyle.Render(formatTokens(usage.TokensOut)))

		if pricing != nil {
			cost := calculateCost(usage.TokensIn, usage.TokensOut, pricing)
			totalCost += cost
			fmt.Fprintf(w, "    %s %s\n",
				labelStyle.Render("Cost:"),
				costStyle.Render(fmt.Sprintf("$%.4f", cost)))
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", headerStyle.Render("Total:"))
	fmt.Fprintf(w, "    %s %s\n",
		labelStyle.Render("Input:"),
		valueStyle.Render(formatTokens(totalIn)))
	fmt.Fprintf(w, "    %s %s\n",
		labelStyle.Render("Output:"),
		valueStyle.Render(formatTokens(totalOut)))

	if pricing != nil {
		fmt.Fprintf(w, "    %s %s\n",
			labelStyle.Render("Total Cost:"),
			costStyle.Render(fmt.Sprintf("$%.4f", totalCost)))
	}
	fmt.Fprintln(w)
}

// calculateCost calculates cost from tokens and pricing.
func calculateCost(tokensIn, tokensOut int64, pricing *Pricing) float64 {
	inCost := float64(tokensIn) / 1_000_000 * pricing.InputPer1M
	outCost := float64(tokensOut) / 1_000_000 * pricing.OutputPer1M
	return inCost + outCost
}

// formatTokens formats token count with comma separators.
func formatTokens(tokens int64) string {
	s := fmt.Sprintf("%d", tokens)
	if tokens < 1000 {
		return s + " tokens"
	}
	// Add comma separators
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result) + " tokens"
}

// formatDuration formats milliseconds as human-readable duration.
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60000 {
		return fmt.Sprintf("%.2fs", float64(ms)/1000)
	}
	mins := ms / 60000
	secs := (ms % 60000) / 1000
	return fmt.Sprintf("%dm%ds", mins, secs)
}
