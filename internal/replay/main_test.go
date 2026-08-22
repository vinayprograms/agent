package replay

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestMain strips ANSI color codes from lipgloss output so golden files
// hold plain text, matching what `--no-pager | grep ...` users see when
// NO_COLOR/non-tty output disables color.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}
