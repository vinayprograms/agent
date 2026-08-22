// Package term reports whether a stream is an interactive terminal.
package term

import "os"

// IsTerminal reports whether v — a stdin, stdout or stderr stream, of either
// reader or writer type — is an interactive terminal. Anything that is not an
// *os.File on a character device (a pipe, a file, a test buffer) is not.
func IsTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
