// Package replay provides session replay and visualization.
package replay

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

// Pager is an interactive terminal pager for session replay.
type pager struct {
	viewport viewport.Model
	title    string
	ready    bool
}

// pagerStyle for the header/footer
var (
	pagerTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	pagerInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	pagerHelpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))
)

// NewPager creates a new interactive pager with the given content.
func NewPager(title, content string) *pager {
	return &pager{
		title: title,
	}
}

// Run starts the interactive pager.
func (p *pager) Run(content string) error {
	prog := tea.NewProgram(
		&pagerModel{
			title:   p.title,
			content: content,
		},
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err := prog.Run()
	return err
}

// pagerModel is the Bubble Tea model for the pager.
type pagerModel struct {
	viewport viewport.Model
	title    string
	content  string
	ready    bool
}

func (m *pagerModel) Init() tea.Cmd {
	return nil
}

func (m *pagerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "g":
			// Go to top
			m.viewport.GotoTop()
		case "G":
			// Go to bottom
			m.viewport.GotoBottom()
		}

	case tea.WindowSizeMsg:
		headerHeight := 1
		footerHeight := 1

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-headerHeight-footerHeight)
			m.viewport.YPosition = headerHeight
			// Wrap content to terminal width
			wrapped := wrapContent(m.content, msg.Width)
			m.viewport.SetContent(wrapped)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - headerHeight - footerHeight
			// Re-wrap on resize
			wrapped := wrapContent(m.content, msg.Width)
			m.viewport.SetContent(wrapped)
		}
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *pagerModel) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	// Header
	title := pagerTitleStyle.Render(m.title)
	line := strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(title)))
	header := lipgloss.JoinHorizontal(lipgloss.Center, title, pagerInfoStyle.Render(line))

	// Footer with scroll position and help
	percent := 0
	if m.viewport.TotalLineCount() > 0 {
		percent = int(float64(m.viewport.YOffset) / float64(max(1, m.viewport.TotalLineCount()-m.viewport.Height)) * 100)
	}
	if percent > 100 {
		percent = 100
	}
	if m.viewport.TotalLineCount() <= m.viewport.Height {
		percent = 100
	}

	info := fmt.Sprintf(" %d%% ", percent)
	help := " q: quit │ ↑/↓: scroll │ g/G: top/bottom │ pgup/pgdn "
	footer := pagerHelpStyle.Render(help) + pagerInfoStyle.Render(strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(help)-lipgloss.Width(info)))) + pagerInfoStyle.Render(info)

	return header + "\n" + m.viewport.View() + "\n" + footer
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// wrapContent wraps each line to fit within the given width.
// Preserves ANSI escape codes and handles indentation.
func wrapContent(content string, width int) string {
	if width <= 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	var result []string

	for _, line := range lines {
		if lipgloss.Width(line) <= width {
			result = append(result, line)
		} else {
			// Use wordwrap which handles ANSI codes
			wrapped := wordwrap.String(line, width)
			result = append(result, strings.Split(wrapped, "\n")...)
		}
	}

	return strings.Join(result, "\n")
}
