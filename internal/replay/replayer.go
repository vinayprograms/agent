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
type Replayer struct {
	output         io.Writer
	verbosity      int      // 0=normal, 1=verbose (-v), 2=very verbose (-vv)
	maxContentSize int      // Maximum size for Content fields (0 = unlimited)
	pricing        *Pricing // Optional pricing for cost calculation
}

// ReplayerOption configures a Replayer.
type ReplayerOption func(*Replayer)

// WithMaxContentSize limits Content field size to avoid OOM on large sessions.
func WithMaxContentSize(size int) ReplayerOption {
	return func(r *Replayer) {
		r.maxContentSize = size
	}
}

// WithPricing enables cost calculation with the given pricing.
func WithPricing(inputPer1M, outputPer1M float64) ReplayerOption {
	return func(r *Replayer) {
		r.pricing = &Pricing{
			InputPer1M:  inputPer1M,
			OutputPer1M: outputPer1M,
		}
	}
}

// New creates a new Replayer.
func New(output io.Writer, verbosity int, opts ...ReplayerOption) *Replayer {
	r := &Replayer{
		output:         output,
		verbosity:      verbosity,
		maxContentSize: 50 * 1024, // Default: 50KB per content field
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ReplayFile loads and replays a session from a file.
func (r *Replayer) ReplayFile(path string) error {
	sess, err := r.loadSession(path)
	if err != nil {
		return err
	}
	return r.Replay(sess)
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
	oldOutput := r.output
	r.output = &buf

	if err := r.Replay(sess); err != nil {
		r.output = oldOutput
		return err
	}
	r.output = oldOutput

	title := fmt.Sprintf("Session: %s", sess.ID)
	p := NewPager(title, buf.String())
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
		oldOutput := r.output
		r.output = &buf
		err = r.Replay(sess)
		r.output = oldOutput

		if err != nil {
			return "", err
		}
		return buf.String(), nil
	}

	sess, err := r.loadSession(path)
	if err != nil {
		return err
	}

	title := fmt.Sprintf("Session: %s (LIVE)", sess.ID)
	p := NewPager(title, "")
	return p.RunLive(path, renderFunc)
}

// Replay outputs a formatted timeline of session events.
func (r *Replayer) Replay(sess *session.Session) error {
	r.printHeader(sess)
	r.printTimeline(sess)
	r.printSummary(sess)
	return nil
}

func (r *Replayer) printHeader(sess *session.Session) {
	fmt.Fprintln(r.output)
	fmt.Fprintf(r.output, "%s %s\n", titleStyle.Render("SESSION"), valueStyle.Render(sess.ID))
	fmt.Fprintln(r.output, divider)
	fmt.Fprintf(r.output, "%s %s\n", labelStyle.Render("Workflow:"), valueStyle.Render(sess.WorkflowName))
	fmt.Fprintf(r.output, "%s %s\n", labelStyle.Render("Status:  "), r.statusStyle(sess.Status).Render(sess.Status))
	fmt.Fprintf(r.output, "%s %s\n", labelStyle.Render("Created: "), valueStyle.Render(sess.CreatedAt.Format(time.RFC3339)))
	if len(sess.Inputs) > 0 {
		fmt.Fprintf(r.output, "%s %s\n", labelStyle.Render("Inputs:  "), valueStyle.Render(formatMap(sess.Inputs)))
	}
	fmt.Fprintln(r.output)
}

func (r *Replayer) printTimeline(sess *session.Session) {
	fmt.Fprintf(r.output, "%s %s\n", titleStyle.Render("TIMELINE"), dimStyle.Render(fmt.Sprintf("(%d events)", len(sess.Events))))
	fmt.Fprintln(r.output, divider)

	var lastGoal string
	for i, event := range sess.Events {
		r.formatEvent(i+1, &event, &lastGoal)
	}
}

func (r *Replayer) printSummary(sess *session.Session) {
	fmt.Fprintln(r.output)
	fmt.Fprintln(r.output, divider)

	switch sess.Status {
	case session.StatusComplete:
		fmt.Fprintln(r.output, successStyle.Render("COMPLETED"))
	case session.StatusFailed:
		fmt.Fprintf(r.output, "%s %s\n", errorStyle.Render("FAILED:"), valueStyle.Render(sess.Error))
	default:
		fmt.Fprintln(r.output, warnStyle.Render("RUNNING"))
	}

	stats := ComputeStats(sess)
	PrintStats(r.output, stats)
	PrintTokenUsage(r.output, stats, r.pricing)
}
