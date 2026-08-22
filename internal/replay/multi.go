package replay

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinayprograms/agent/internal/session"
)

// MultiReplayer handles multiple session files.
type MultiReplayer struct {
	verbosity int              // 0=normal, 1=verbose (-v), 2=very verbose (-vv)
	opts      []ReplayerOption // Options to pass to internal Replayer
}

// NewMulti creates a new MultiReplayer.
// verbosity: 0=normal, 1=verbose (-v), 2=very verbose (-vv)
func NewMulti(verbosity int, opts ...ReplayerOption) *MultiReplayer {
	return &MultiReplayer{
		verbosity: verbosity,
		opts:      opts,
	}
}

// sessionInfo holds parsed session with source info.
type sessionInfo struct {
	Session   *session.Session
	Source    string // Original file path
	AgentName string // Extracted or inferred agent name
}

// ReplayFiles renders multiple sessions into w.
func (m *MultiReplayer) ReplayFiles(w io.Writer, paths []string) error {
	sessions, err := m.loadSessions(paths)
	if err != nil {
		return err
	}

	return m.replayAll(w, sessions)
}

// ReplayFilesInteractive shows multiple sessions in the interactive pager.
func (m *MultiReplayer) ReplayFilesInteractive(paths []string) error {
	sessions, err := m.loadSessions(paths)
	if err != nil {
		return err
	}

	var buf strings.Builder
	if err := m.replayAll(&buf, sessions); err != nil {
		return err
	}

	// Build title
	title := fmt.Sprintf("%d session(s)", len(sessions))
	if len(sessions) == 1 {
		title = sessions[0].AgentName
	}

	p := newPager(title)
	return p.Run(buf.String())
}

// loadSessions loads and parses all session files.
func (m *MultiReplayer) loadSessions(paths []string) ([]sessionInfo, error) {
	var sessions []sessionInfo

	// Create a temporary replayer for loading (uses format detection)
	loader := New(m.verbosity, m.opts...)

	for _, path := range paths {
		sess, err := loader.loadSession(path)
		if err != nil {
			return nil, fmt.Errorf("failed to load %s: %w", path, err)
		}

		info := sessionInfo{
			Session:   sess,
			Source:    path,
			AgentName: inferAgentName(sess, path),
		}
		sessions = append(sessions, info)
	}

	// Sort by creation time
	slices.SortFunc(sessions, func(a, b sessionInfo) int {
		return a.Session.CreatedAt.Compare(b.Session.CreatedAt)
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

// replayAll renders all sessions into w.
func (m *MultiReplayer) replayAll(w io.Writer, sessions []sessionInfo) error {
	r := New(m.verbosity, m.opts...)

	for i, info := range sessions {
		if len(sessions) > 1 {
			printSessionHeader(w, info, i+1, len(sessions))
		}

		if err := r.Replay(w, info.Session); err != nil {
			return fmt.Errorf("failed to replay %s: %w", info.Source, err)
		}

		// Add spacing between sessions
		if i < len(sessions)-1 {
			fmt.Fprintln(w)
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
func printSessionHeader(w io.Writer, info sessionInfo, num, total int) {
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
	fmt.Fprintln(w)
	fmt.Fprintln(w, sessionDividerStyle.Render(strings.Repeat("━", 70)))
	fmt.Fprintln(w, sessionHeaderStyle.Render(header))
	fmt.Fprintln(w, sessionDividerStyle.Render(strings.Repeat("━", 70)))
}
