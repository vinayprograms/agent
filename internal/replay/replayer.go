// Package replay provides session replay and visualization.
package replay

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vinayprograms/agent/internal/session"
)

// Replayer reads and formats session events for forensic analysis.
// A Replayer holds no writer state: Replay and its variants render into the
// io.Writer passed to each call, so a single Replayer is safe to reuse and
// to share across goroutines.
type Replayer struct {
	verbosity      int        // 0=normal, 1=verbose (-v), 2=very verbose (-vv)
	maxContentSize int        // Maximum size for Content fields (0 = unlimited)
	pricing        PricingMap // Optional per-model pricing for cost calculation
}

// ReplayerOption configures a Replayer.
type ReplayerOption func(*Replayer)

// MaxContentSize limits Content field size to avoid OOM on large sessions.
func MaxContentSize(size int) ReplayerOption {
	return func(r *Replayer) {
		r.maxContentSize = size
	}
}

// Pricing adds pricing for a specific model (per 1M tokens).
func Pricing(model string, inputPer1M, outputPer1M float64) ReplayerOption {
	return func(r *Replayer) {
		if r.pricing == nil {
			r.pricing = make(PricingMap)
		}
		r.pricing[model] = &ModelPricing{
			InputPer1M:  inputPer1M,
			OutputPer1M: outputPer1M,
		}
	}
}

// New creates a new Replayer.
func New(verbosity int, opts ...ReplayerOption) *Replayer {
	r := &Replayer{
		verbosity:      verbosity,
		maxContentSize: 50 * 1024, // Default: 50KB per content field
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ReplayFile loads and replays a session from a file into w.
func (r *Replayer) ReplayFile(w io.Writer, path string) error {
	sess, err := r.loadSession(path)
	if err != nil {
		return err
	}
	return r.Replay(w, sess)
}

// ReplayFileInteractive loads and replays with interactive pager.
func (r *Replayer) ReplayFileInteractive(path string) error {
	sess, err := r.loadSession(path)
	if err != nil {
		return err
	}
	return r.ReplayInteractive(sess)
}

// ReplayInteractive outputs a formatted timeline using an interactive pager.
func (r *Replayer) ReplayInteractive(sess *session.Session) error {
	var buf strings.Builder
	if err := r.Replay(&buf, sess); err != nil {
		return err
	}

	title := fmt.Sprintf("Session: %s", sess.ID)
	p := newPager(title)
	return p.Run(buf.String())
}

// ReplayFileLive loads and replays with live file watching.
func (r *Replayer) ReplayFileLive(path string) error {
	renderFunc := func() (string, error) {
		sess, err := r.loadSession(path)
		if err != nil {
			return "", err
		}

		var buf strings.Builder
		if err := r.Replay(&buf, sess); err != nil {
			return "", err
		}
		return buf.String(), nil
	}

	sess, err := r.loadSession(path)
	if err != nil {
		return err
	}

	title := fmt.Sprintf("Session: %s (LIVE)", sess.ID)
	p := newPager(title)
	return p.RunLive(path, renderFunc)
}

// Replay renders a formatted timeline of session events into w.
func (r *Replayer) Replay(w io.Writer, sess *session.Session) error {
	r.printHeader(w, sess)
	r.printTimeline(w, sess)
	r.printSummary(w, sess)
	return nil
}

func (r *Replayer) printHeader(w io.Writer, sess *session.Session) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s %s\n", titleStyle.Render("SESSION"), valueStyle.Render(sess.ID))
	fmt.Fprintln(w, divider)
	fmt.Fprintf(w, "%s %s\n", labelStyle.Render("Workflow:"), valueStyle.Render(sess.WorkflowName))
	fmt.Fprintf(w, "%s %s\n", labelStyle.Render("Status:  "), r.statusStyle(sess.Status).Render(sess.Status))
	fmt.Fprintf(w, "%s %s\n", labelStyle.Render("Created: "), valueStyle.Render(sess.CreatedAt.Format(time.RFC3339)))
	if len(sess.Inputs) > 0 {
		fmt.Fprintf(w, "%s %s\n", labelStyle.Render("Inputs:  "), valueStyle.Render(formatMap(sess.Inputs)))
	}
	fmt.Fprintln(w)
}

func (r *Replayer) printTimeline(w io.Writer, sess *session.Session) {
	fmt.Fprintf(w, "%s %s\n", titleStyle.Render("TIMELINE"), dimStyle.Render(fmt.Sprintf("(%d events)", len(sess.Events))))
	fmt.Fprintln(w, divider)

	var lastGoal string
	for i, event := range sess.Events {
		r.formatEvent(w, i+1, &event, &lastGoal)
	}
}

func (r *Replayer) printSummary(w io.Writer, sess *session.Session) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, divider)

	switch sess.Status {
	case session.StatusComplete:
		fmt.Fprintln(w, successStyle.Render("COMPLETED"))
	case session.StatusFailed:
		fmt.Fprintf(w, "%s %s\n", errorStyle.Render("FAILED:"), valueStyle.Render(sess.Error))
	default:
		fmt.Fprintln(w, warnStyle.Render("RUNNING"))
	}

	stats := ComputeStats(sess)
	PrintStats(w, stats)
	PrintTokenUsage(w, stats, r.pricing)
}
