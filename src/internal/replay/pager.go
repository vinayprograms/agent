// Package replay provides session replay and visualization.
package replay

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
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

// RunLive starts the interactive pager with live file watching.
func (p *pager) RunLive(filePath string, renderFunc func() (string, error)) error {
	// Initial render
	content, err := renderFunc()
	if err != nil {
		return err
	}

	// Set up file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	if err := watcher.Add(filePath); err != nil {
		watcher.Close()
		return fmt.Errorf("failed to watch file: %w", err)
	}

	prog := tea.NewProgram(
		&pagerModel{
			title:      p.title,
			content:    content,
			live:       true,
			renderFunc: renderFunc,
			watcher:    watcher,
		},
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err = prog.Run()
	watcher.Close()
	return err
}

// fileChangedMsg is sent when the watched file changes.
type fileChangedMsg struct{}

// pagerModel is the Bubble Tea model for the pager.
type pagerModel struct {
	viewport       viewport.Model
	title          string
	content        string
	wrappedContent string // Wrapped content for accurate line searching
	ready          bool
	live           bool
	renderFunc     func() (string, error)
	watcher        *fsnotify.Watcher
	lastUpdate     time.Time
	eventCount     int // Track event count to show in live mode

	// Search state
	searching    bool
	searchInput  textinput.Model
	searchQuery  string
	searchLines  []int // Line numbers matching search (in wrapped content)
	searchIndex  int   // Current match index
	searchFailed bool  // No matches found
}

func (m *pagerModel) Init() tea.Cmd {
	if m.live && m.watcher != nil {
		return m.watchFile()
	}
	return nil
}

// watchFile returns a command that waits for file changes.
func (m *pagerModel) watchFile() tea.Cmd {
	return func() tea.Msg {
		for {
			select {
			case event, ok := <-m.watcher.Events:
				if !ok {
					return nil
				}
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					// Debounce: wait a bit for writes to settle
					time.Sleep(100 * time.Millisecond)
					return fileChangedMsg{}
				}
			case _, ok := <-m.watcher.Errors:
				if !ok {
					return nil
				}
				// Ignore errors, keep watching
			}
		}
	}
}

func (m *pagerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	// Handle search input mode
	if m.searching {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter":
				// Execute search
				m.searchQuery = m.searchInput.Value()
				m.searching = false
				m.executeSearch()
				if len(m.searchLines) > 0 {
					m.jumpToMatch(0)
				}
				return m, nil
			case "esc", "ctrl+c":
				// Cancel search
				m.searching = false
				m.searchQuery = ""
				m.searchLines = nil
				m.searchFailed = false
				return m, nil
			}
		}
		m.searchInput, cmd = m.searchInput.Update(msg)
		return m, cmd
	}

	switch msg := msg.(type) {
	case fileChangedMsg:
		// File changed - reload content but preserve scroll position
		if m.renderFunc != nil {
			if newContent, err := m.renderFunc(); err == nil {
				oldOffset := m.viewport.YOffset
				oldLineCount := m.viewport.TotalLineCount()
				
				m.content = newContent
				m.wrappedContent = wrapContent(m.content, m.viewport.Width)
				m.viewport.SetContent(m.wrappedContent)
				m.lastUpdate = time.Now()
				
				// Try to preserve position
				newLineCount := m.viewport.TotalLineCount()
				if oldOffset <= newLineCount-m.viewport.Height {
					m.viewport.YOffset = oldOffset
				} else if oldOffset > 0 && newLineCount > oldLineCount {
					// Content grew - stay at same position
					m.viewport.YOffset = oldOffset
				}
				
				// Re-run search if active
				if m.searchQuery != "" {
					m.executeSearch()
				}
			}
		}
		// Continue watching
		cmds = append(cmds, m.watchFile())

	case tea.KeyMsg:
		// Ignore modifier-only key presses (cmd, alt, ctrl, shift by themselves)
		keyStr := msg.String()
		if keyStr == "" || keyStr == "ctrl" || keyStr == "alt" || keyStr == "shift" || keyStr == "super" {
			return m, nil
		}
		
		switch keyStr {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			// Clear search highlight if searching, otherwise quit
			if m.searchQuery != "" {
				m.searchQuery = ""
				m.searchLines = nil
				m.searchFailed = false
			} else {
				return m, tea.Quit
			}
		case "g":
			// Go to top
			m.viewport.GotoTop()
		case "G":
			// Go to bottom
			m.viewport.GotoBottom()
		case "f", "F":
			// Follow mode - jump to bottom (useful in live mode)
			if m.live {
				m.viewport.GotoBottom()
			}
		case "/":
			// Start search
			m.searching = true
			m.searchInput = textinput.New()
			m.searchInput.Placeholder = "Search..."
			m.searchInput.Focus()
			m.searchInput.CharLimit = 100
			m.searchInput.Width = 40
			if m.searchQuery != "" {
				m.searchInput.SetValue(m.searchQuery)
			}
			return m, textinput.Blink
		case "n":
			// Next match
			if len(m.searchLines) > 0 {
				m.searchIndex = (m.searchIndex + 1) % len(m.searchLines)
				m.jumpToMatch(m.searchIndex)
			}
		case "N":
			// Previous match
			if len(m.searchLines) > 0 {
				m.searchIndex--
				if m.searchIndex < 0 {
					m.searchIndex = len(m.searchLines) - 1
				}
				m.jumpToMatch(m.searchIndex)
			}
		}

	case tea.WindowSizeMsg:
		headerHeight := 1
		footerHeight := 1

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-headerHeight-footerHeight)
			m.viewport.YPosition = headerHeight
			// Wrap content to terminal width
			m.wrappedContent = wrapContent(m.content, msg.Width)
			m.viewport.SetContent(m.wrappedContent)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - headerHeight - footerHeight
			// Re-wrap on resize
			m.wrappedContent = wrapContent(m.content, msg.Width)
			m.viewport.SetContent(m.wrappedContent)
			// Re-run search if active (line numbers may have changed)
			if m.searchQuery != "" {
				m.executeSearch()
			}
		}
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// executeSearch finds all lines matching the search query in wrapped content.
func (m *pagerModel) executeSearch() {
	m.searchLines = nil
	m.searchIndex = 0
	m.searchFailed = false

	if m.searchQuery == "" {
		return
	}

	query := strings.ToLower(m.searchQuery)
	
	// Search the wrapped content (what's actually displayed)
	contentLines := strings.Split(m.wrappedContent, "\n")
	
	for i, line := range contentLines {
		if strings.Contains(strings.ToLower(line), query) {
			m.searchLines = append(m.searchLines, i)
		}
	}

	if len(m.searchLines) == 0 {
		m.searchFailed = true
	}
}

// jumpToMatch scrolls to the given match index.
func (m *pagerModel) jumpToMatch(index int) {
	if index < 0 || index >= len(m.searchLines) {
		return
	}
	
	lineNum := m.searchLines[index]
	// Center the match on screen if possible
	targetOffset := lineNum - m.viewport.Height/2
	if targetOffset < 0 {
		targetOffset = 0
	}
	maxOffset := m.viewport.TotalLineCount() - m.viewport.Height
	if targetOffset > maxOffset {
		targetOffset = maxOffset
	}
	if maxOffset < 0 {
		targetOffset = 0
	}
	m.viewport.YOffset = targetOffset
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
	
	var footer string
	
	if m.searching {
		// Search input mode
		searchPrompt := lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Render("/")
		footer = searchPrompt + m.searchInput.View()
	} else {
		// Build help text based on mode and state
		var help string
		
		if m.searchFailed {
			notFound := lipgloss.NewStyle().
				Foreground(lipgloss.Color("9")).
				Render("Pattern not found")
			help = fmt.Sprintf(" %s │ /: search ", notFound)
		} else if len(m.searchLines) > 0 {
			matchInfo := lipgloss.NewStyle().
				Foreground(lipgloss.Color("11")).
				Render(fmt.Sprintf("[%d/%d]", m.searchIndex+1, len(m.searchLines)))
			help = fmt.Sprintf(" %s │ n/N: next/prev │ /: search │ esc: clear ", matchInfo)
		} else if m.live {
			liveIndicator := lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("10")).
				Render("● LIVE")
			help = fmt.Sprintf(" %s │ q: quit │ /: search │ f: follow │ g/G: top/bottom ", liveIndicator)
		} else {
			help = " q: quit │ /: search │ n/N: next/prev │ g/G: top/bottom "
		}
		
		footer = pagerHelpStyle.Render(help) + pagerInfoStyle.Render(strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(help)-lipgloss.Width(info)))) + pagerInfoStyle.Render(info)
	}

	return header + "\n" + m.viewport.View() + "\n" + footer
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// wrapContent wraps each line to fit within the given width.
// Preserves ANSI escape codes and maintains table column alignment.
func wrapContent(content string, width int) string {
	if width <= 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	var result []string

	for _, line := range lines {
		if lipgloss.Width(line) <= width {
			result = append(result, line)
			continue
		}

		// Detect if this is a table row (has │ separators)
		// Format: "  seq │ timestamp │ content"
		if strings.Contains(line, "│") {
			// Find the last │ and calculate indent for continuation
			lastPipe := strings.LastIndex(line, "│")
			if lastPipe > 0 && lastPipe < len(line)-1 {
				// Calculate visual width of prefix (up to and including last │ plus space)
				prefix := line[:lastPipe+1]
				prefixWidth := lipgloss.Width(prefix) + 1 // +1 for space after │
				
				// Calculate available content width
				contentWidth := width - prefixWidth
				if contentWidth < 20 {
					contentWidth = 20 // Minimum content width
				}
				
				// Extract content after the last │
				contentStart := lastPipe + 1
				// Skip leading space
				for contentStart < len(line) && line[contentStart] == ' ' {
					contentStart++
				}
				contentPart := line[contentStart:]
				
				// Wrap the content portion
				wrapped := wordwrap.String(contentPart, contentWidth)
				wrappedLines := strings.Split(wrapped, "\n")
				
				// Build continuation indent (spaces to align with content column)
				contIndent := strings.Repeat(" ", prefixWidth)
				
				// First line keeps original prefix
				result = append(result, line[:contentStart]+wrappedLines[0])
				
				// Continuation lines get the indent
				for i := 1; i < len(wrappedLines); i++ {
					result = append(result, contIndent+wrappedLines[i])
				}
				continue
			}
		}
		
		// Non-table line: simple wrap
		wrapped := wordwrap.String(line, width)
		result = append(result, strings.Split(wrapped, "\n")...)
	}

	return strings.Join(result, "\n")
}
