package term

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestIsTerminal(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "f"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	closed, err := os.Open(f.Name())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	closed.Close()

	for _, tc := range []struct {
		name string
		in   any
		want bool
	}{
		{"buffer is not a terminal", &bytes.Buffer{}, false},
		{"regular file is not a terminal", f, false},
		{"unstattable file is not a terminal", closed, false},
		{"a character device is a terminal", devNull(t), true},
	} {
		if got := IsTerminal(tc.in); got != tc.want {
			t.Errorf("%s: IsTerminal() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// devNull is a character device, the only kind IsTerminal accepts.
func devNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
