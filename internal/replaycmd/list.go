package replaycmd

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/vinayprograms/agent/internal/session"
)

// timeLayout is how a session's creation time is shown, in local time.
const timeLayout = "2006-01-02 15:04:05"

// writeTable prints one row per session: id, creation time, workflow
// name, label, status and how long the run took.
func writeTable(w io.Writer, sessions []session.Summary) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCREATED\tNAME\tLABEL\tSTATUS\tDURATION")
	for _, s := range sessions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.ID, s.CreatedAt.Local().Format(timeLayout),
			valueOr(s.Name, "-"), valueOr(s.Label, "-"), valueOr(s.Status, "-"), duration(s))
	}
	tw.Flush()
}

// duration is the time between a session's header and its last footer,
// or "-" when the footer never landed.
func duration(s session.Summary) string {
	if s.UpdatedAt.IsZero() || s.CreatedAt.IsZero() || s.UpdatedAt.Before(s.CreatedAt) {
		return "-"
	}
	return s.UpdatedAt.Sub(s.CreatedAt).Round(time.Second).String()
}
