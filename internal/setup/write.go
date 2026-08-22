package setup

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agent/internal/configfile"
	"github.com/vinayprograms/agentkit/credentials"
)

// Messages
type filesWrittenMsg struct {
	files []string
}

type errMsg struct {
	error error
}

func (m Model) writeFiles() tea.Cmd {
	return func() tea.Msg {
		// The wizard is the user's explicit intent to (re)write these files,
		// so it overwrites; `agent config init` is the guarded entry point.
		files, err := configfile.Write(m.dir, m.config, true)
		if err != nil {
			return errMsg{err}
		}

		// Write credentials to ~/.config/agent/credentials.toml
		if m.config.CredentialMethod == "file" && m.config.APIKey != "" {
			path, err := m.writeCredentials()
			if err != nil {
				return errMsg{err}
			}
			files = append(files, path)
		}

		// claude-cli method doesn't need to write anything - credentials are read from Claude CLI

		return filesWrittenMsg{files}
	}
}

// credentialsPath is ~/.config/agent/credentials.toml, the user-level entry
// of credentials.StandardPaths("agent").
func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(config.DefaultConfigDir(home), "credentials.toml"), nil
}

// writeCredentials saves the API key to ~/.config/agent/credentials.toml,
// preserving any other providers already stored there, and returns the path.
// A load error (e.g. an existing file with insecure permissions, or a
// malformed file) is surfaced rather than silently overwritten.
func (m Model) writeCredentials() (string, error) {
	path, err := credentialsPath()
	if err != nil {
		return "", err
	}
	store, err := credentials.NewFileStore(path)
	if err != nil {
		return "", fmt.Errorf("load credentials: %w", err)
	}
	if store == nil { // an empty file yields a nil store
		store = credentials.FileStore{}
	}
	store.SetAPIKey(m.config.Provider, m.config.APIKey)
	if err := store.Save(path); err != nil {
		return "", fmt.Errorf("save credentials: %w", err)
	}
	return path, nil
}
