package replay

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinayprograms/agent/internal/session"
)

// MultiReplayer handles multiple session files.
type MultiReplayer struct {
	output  io.Writer
	verbose bool
}

// NewMulti creates a new MultiReplayer.
func NewMulti(output io.Writer, verbose bool) *MultiReplayer {
	return &MultiReplayer{
		output:  output,
		verbose: verbose,
	}
}

// sessionInfo holds parsed session with source info.
type sessionInfo struct {
	Session   *session.Session
	Source    string // Original file path
	AgentName string // Extracted or inferred agent name
}

// ReplayFiles outputs multiple sessions to the writer.
func (m *MultiReplayer) ReplayFiles(paths []string) error {
	sessions, err := m.loadSessions(paths)
	if err != nil {
		return err
	}

	return m.replayAll(sessions)
}

// ReplayFilesInteractive shows multiple sessions in the interactive pager.
func (m *MultiReplayer) ReplayFilesInteractive(paths []string) error {
	sessions, err := m.loadSessions(paths)
	if err != nil {
		return err
	}

	// Render to string
	var buf strings.Builder
	oldOutput := m.output
	m.output = &buf

	if err := m.replayAll(sessions); err != nil {
		m.output = oldOutput
		return err
	}
	m.output = oldOutput

	// Build title
	title := fmt.Sprintf("%d session(s)", len(sessions))
	if len(sessions) == 1 {
		title = sessions[0].AgentName
	}

	p := NewPager(title, buf.String())
	return p.Run(buf.String())
}

// loadSessions loads and parses all session files.
func (m *MultiReplayer) loadSessions(paths []string) ([]sessionInfo, error) {
	var sessions []sessionInfo

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", path, err)
		}

		var sess session.Session
		if err := json.Unmarshal(data, &sess); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", path, err)
		}

		info := sessionInfo{
			Session:   &sess,
			Source:    path,
			AgentName: inferAgentName(&sess, path),
		}
		sessions = append(sessions, info)
	}

	// Sort by creation time
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Session.CreatedAt.Before(sessions[j].Session.CreatedAt)
	})

	return sessions, nil
}

// inferAgentName extracts agent name from session or filename.
func inferAgentName(sess *session.Session, path string) string {
	// Try workflow name first (often the agent's role)
	if sess.WorkflowName != "" {
		return sess.WorkflowName
	}

	// Fall back to filename without extension
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// replayAll renders all sessions.
func (m *MultiReplayer) replayAll(sessions []sessionInfo) error {
	r := New(m.output, m.verbose)

	for i, info := range sessions {
		if len(sessions) > 1 {
			m.printSessionHeader(info, i+1, len(sessions))
		}

		if err := r.Replay(info.Session); err != nil {
			return fmt.Errorf("failed to replay %s: %w", info.Source, err)
		}

		// Add spacing between sessions
		if i < len(sessions)-1 {
			fmt.Fprintln(m.output)
		}
	}

	return nil
}

// Session header styles
var (
	sessionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("6")) // Cyan background

	sessionDividerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")) // Cyan
)

// printSessionHeader prints a distinctive header for each session.
func (m *MultiReplayer) printSessionHeader(info sessionInfo, num, total int) {
	// Short session ID (first 12 chars)
	shortID := info.Session.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}

	// Build header content
	header := fmt.Sprintf(" %s │ %s │ %s ",
		info.AgentName,
		shortID,
		info.Session.CreatedAt.Format("2006-01-02 15:04:05"))

	if total > 1 {
		header = fmt.Sprintf(" [%d/%d] %s", num, total, header)
	}

	// Print with styling
	fmt.Fprintln(m.output)
	fmt.Fprintln(m.output, sessionDividerStyle.Render(strings.Repeat("━", 70)))
	fmt.Fprintln(m.output, sessionHeaderStyle.Render(header))
	fmt.Fprintln(m.output, sessionDividerStyle.Render(strings.Repeat("━", 70)))
}
