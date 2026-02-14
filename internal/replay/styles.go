// Package replay provides session replay and visualization.
package replay

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Component color scheme - each component has a distinct, consistent color.
var (
	// Structural / metadata
	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // Gray - timestamps, metadata

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // Gray - labels

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")) // White - values

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")) // White bold - headers

	// Main agent flow - default/white
	flowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")) // White

	// Tools - Blue
	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")) // Blue

	// Execution supervisor - Yellow
	execSupervisorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("11")) // Yellow

	// Security (supervisor + checks) - Cyan
	securityStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")) // Cyan

	securitySupervisorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("14")) // Cyan bold

	// Bash security - Orange (distinct from general security)
	bashStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("208")) // Orange

	// Sub-agents - Magenta
	subagentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")) // Magenta

	subagentDimStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("5")) // Magenta dim

	// Outcomes
	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")) // Green

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")) // Red

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")) // Yellow

	// Timeline
	seqStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Width(5).
			Align(lipgloss.Right)

	timeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	// Content blocks
	blockHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Italic(true)

	divider = lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Render(strings.Repeat("━", 60))
)
