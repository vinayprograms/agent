package replaycmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vinayprograms/agent/internal/session"
)

// filter is the set of session selectors the command exposes. The zero
// value matches every session.
type filter struct {
	last      bool
	name      string
	agentfile string
	label     string
	status    string
	since     time.Duration
}

// active reports whether any selector was given.
func (f filter) active() bool {
	return f.last || f.name != "" || f.agentfile != "" || f.label != "" || f.status != "" || f.since != 0
}

// match reports whether s satisfies every selector but --last, which
// applies to the filtered set rather than to one session.
func (f filter) match(s session.Summary, now time.Time) bool {
	switch {
	case f.name != "" && s.Name != f.name:
		return false
	case f.label != "" && s.Label != f.label:
		return false
	case f.status != "" && s.Status != f.status:
		return false
	case f.since != 0 && s.CreatedAt.Before(now.Add(-f.since)):
		return false
	case f.agentfile != "" && s.Agentfile != f.agentfile && filepath.Base(s.Agentfile) != f.agentfile:
		return false
	}
	return true
}

// apply filters sessions (already in created order) and honours --last.
func (f filter) apply(sessions []session.Summary) []session.Summary {
	now := time.Now()
	var out []session.Summary
	for _, s := range sessions {
		if f.match(s, now) {
			out = append(out, s)
		}
	}
	if f.last && len(out) > 0 {
		out = out[len(out)-1:]
	}
	return out
}

// store is the recorded sessions of one state directory.
type store struct {
	dir      string // <state>/sessions
	sessions []session.Summary
}

// openStore summarises every session under stateDir, reporting any
// unreadable files to warn. A state directory that holds no sessions yet
// is not an error — the caller reports it.
func openStore(stateDir string, warn io.Writer) *store {
	dir := filepath.Join(stateDir, "sessions")
	return &store{dir: dir, sessions: findSessions(dir, warn)}
}

// findSessions summarises every session under dir, reporting the files it
// could not read to warn rather than failing over them.
func findSessions(dir string, warn io.Writer) []session.Summary {
	sessions, err := session.Find(dir)
	if err != nil {
		fmt.Fprintf(warn, "warning: skipped unreadable session files:\n  %s\n", strings.ReplaceAll(err.Error(), "\n", "\n  "))
	}
	return sessions
}

// empty reports whether the store holds nothing to replay or list.
func (s *store) empty() bool { return len(s.sessions) == 0 }

// resolve turns one positional argument into sessions: an existing path
// (a session file, or a directory to walk), else a session id or id
// prefix. Unreadable files inside a directory are reported to warn and
// skipped, exactly as they are when scanning the state directory.
func (s *store) resolve(arg string, warn io.Writer) ([]session.Summary, error) {
	info, err := os.Stat(arg)
	switch {
	case err == nil && info.IsDir():
		found := findSessions(arg, warn)
		if len(found) == 0 {
			return nil, fmt.Errorf("no session files in %s", arg)
		}
		return found, nil
	case err == nil:
		sum, err := session.Summarize(arg)
		if err != nil {
			return nil, err
		}
		return []session.Summary{sum}, nil
	}
	var matches []session.Summary
	for _, sum := range s.sessions {
		if strings.HasPrefix(sum.ID, arg) {
			matches = append(matches, sum)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no session, file or directory matches %q (looked in %s)", arg, s.dir)
	case 1:
		return matches, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d sessions:", arg, len(matches))
	for _, m := range matches {
		fmt.Fprintf(&b, "\n  %s  %s  %s", m.ID, m.CreatedAt.Local().Format(timeLayout), m.Name)
	}
	return nil, fmt.Errorf("%s\nuse a longer prefix", b.String())
}

// paths returns the files for sums, oldest first — the order sessions
// replay in, matching how the table lists them.
func paths(sums []session.Summary) []string {
	slices.SortFunc(sums, func(a, b session.Summary) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	out := make([]string, len(sums))
	for i, s := range sums {
		out[i] = s.Path
	}
	return out
}
