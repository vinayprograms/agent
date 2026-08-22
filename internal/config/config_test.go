package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestDefaultDirs(t *testing.T) {
	if got, want := DefaultConfigDir("/h"), filepath.Join("/h", ".config", "grid"); got != want {
		t.Errorf("DefaultConfigDir(/h) = %q, want %q", got, want)
	}
	if got, want := DefaultStateDir("/h"), filepath.Join("/h", ".local", "agent"); got != want {
		t.Errorf("DefaultStateDir(/h) = %q, want %q", got, want)
	}
}

func TestNew(t *testing.T) {
	want := &Config{
		LLM:       LLMConfig{MaxTokens: 4096},
		State:     StateConfig{Location: "~/.local/agent"},
		Telemetry: TelemetryConfig{Protocol: ProtocolNoop},
		Timeouts:  TimeoutsConfig{MCP: 60, WebSearch: 30, WebFetch: 60},
	}
	if diff := cmp.Diff(want, New()); diff != "" {
		t.Errorf("New() mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadFile(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		want    func(*Config)
		wantErr string
	}{
		{
			name: "all sections",
			toml: `
[agent]
id = "full-agent"
workspace = "/ws"

[llm]
provider = "openai"
model = "gpt-4o"
api_key_env = "OPENAI_API_KEY"
max_tokens = 8192
base_url = "http://localhost:11434"
thinking = "high"

[profiles.fast]
model = "gpt-4o-mini"

[web]
gateway_url = "https://gw.example.com"
gateway_token_env = "GATEWAY_TOKEN"

[telemetry]
enabled = true
endpoint = "localhost:4317"
protocol = "http"

[state]
location = "/data/grid"

[mcp.servers.fs]
command = "mcp-fs"
args = ["--root", "/"]
`,
			want: func(c *Config) {
				c.Agent = AgentConfig{ID: "full-agent", Workspace: "/ws"}
				c.LLM = LLMConfig{Provider: "openai", Model: "gpt-4o", APIKeyEnv: "OPENAI_API_KEY", MaxTokens: 8192, BaseURL: "http://localhost:11434", Thinking: "high"}
				c.Profiles = map[string]LLMConfig{"fast": {Model: "gpt-4o-mini"}}
				c.Web = WebConfig{GatewayURL: "https://gw.example.com", GatewayTokenEnv: "GATEWAY_TOKEN"}
				c.Telemetry = TelemetryConfig{Enabled: true, Endpoint: "localhost:4317", Protocol: ProtocolHTTP}
				c.State.Location = "/data/grid"
				c.MCP.Servers = map[string]MCPServerConfig{"fs": {Command: "mcp-fs", Args: []string{"--root", "/"}}}
			},
		},
		{
			name: "legacy storage.location",
			toml: "[storage]\nlocation = \"/legacy\"\n",
			want: func(c *Config) {
				c.State.Location = "/legacy"
				c.Deprecations = []string{`[storage] is deprecated, rename to [state] with location = "/legacy"`}
			},
		},
		{
			name: "legacy storage.path",
			toml: "[storage]\npath = \"/legacy-path\"\n",
			want: func(c *Config) {
				c.State.Location = "/legacy-path"
				c.Deprecations = []string{`[storage] is deprecated, rename to [state] with location = "/legacy-path"`}
			},
		},
		{
			name: "empty storage keeps default",
			toml: "[storage]\n",
			want: func(*Config) {},
		},
		{
			name:    "storage and state together",
			toml:    "[storage]\nlocation = \"/a\"\n[state]\nlocation = \"/b\"\n",
			wantErr: "both [state] and [storage]",
		},
		{
			name:    "invalid toml",
			toml:    "[invalid",
			wantErr: "failed to parse config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, filepath.Join(t.TempDir(), "agent.toml"), tt.toml)
			got, err := LoadFile(path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadFile() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadFile() error = %v", err)
			}
			want := New()
			tt.want(want)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("LoadFile() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadFile_Missing(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Error("LoadFile(missing) = nil error, want error")
	}
}

func TestLoadWithPrecedence(t *testing.T) {
	const (
		global  = "[agent]\nid = \"global-id\"\nworkspace = \"/global\"\n[llm]\nmodel = \"global-model\"\nmax_tokens = 111\n"
		project = "[agent]\nworkspace = \"/project\"\n[llm]\nmodel = \"project-model\"\n"
		env     = "[llm]\nmodel = \"env-model\"\nmax_tokens = 222\n"
		cli     = "[llm]\nmodel = \"cli-model\"\n"
	)
	type files struct{ global, project, env, cli string }
	tests := []struct {
		name    string
		files   files
		envVar  string // custom env var name; empty uses EnvConfigPath
		noEnv   bool   // do not point the env var at the env file
		noCLI   bool
		want    func(*Config)
		wantErr string
	}{
		{
			name:  "global < project < env < cli",
			files: files{global, project, env, cli},
			want: func(c *Config) {
				c.Agent = AgentConfig{ID: "global-id", Workspace: "/project"}
				c.LLM = LLMConfig{Model: "cli-model", MaxTokens: 222}
			},
		},
		{
			name:   "custom env var",
			files:  files{env: env},
			envVar: "MY_CONFIG",
			noCLI:  true,
			want:   func(c *Config) { c.LLM = LLMConfig{Model: "env-model", MaxTokens: 222} },
		},
		{
			name:  "missing optional files are ignored",
			noEnv: true,
			noCLI: true,
			want:  func(*Config) {},
		},
		{
			name:    "env file must exist",
			files:   files{},
			noCLI:   true,
			wantErr: "failed to load config from",
		},
		{
			name:    "cli file must exist",
			noEnv:   true,
			wantErr: "failed to parse config",
		},
		{
			name:    "unreadable global path",
			files:   files{global: "\x00notadir"},
			noEnv:   true,
			noCLI:   true,
			wantErr: "not a directory",
		},
		{
			name:    "unreadable project path",
			files:   files{project: "\x00notadir"},
			noEnv:   true,
			noCLI:   true,
			wantErr: "not a directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			home := filepath.Join(tmp, "home")
			projectDir := filepath.Join(tmp, "project")
			envPath := filepath.Join(tmp, "env.toml")
			cliPath := filepath.Join(tmp, "cli.toml")
			// A leading NUL marks "make the parent a regular file" so os.Stat fails with ENOTDIR.
			place := func(path, body string) {
				if strings.HasPrefix(body, "\x00") {
					writeFile(t, filepath.Dir(path), "")
				} else if body != "" {
					writeFile(t, path, body)
				}
			}
			place(filepath.Join(DefaultConfigDir(home), "agent.toml"), tt.files.global)
			place(filepath.Join(projectDir, "agent.toml"), tt.files.project)
			place(envPath, tt.files.env)
			place(cliPath, tt.files.cli)

			envVar := EnvConfigPath
			if tt.envVar != "" {
				envVar = tt.envVar
			}
			environ := map[string]string{}
			if !tt.noEnv {
				environ[envVar] = envPath
			}
			opts := LoadOptions{
				ProjectDir: projectDir,
				EnvVar:     tt.envVar,
				Home:       home,
				Getenv:     func(k string) string { return environ[k] },
			}
			if !tt.noCLI {
				opts.CLIPath = cliPath
			}

			got, err := LoadWithPrecedence(opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadWithPrecedence() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadWithPrecedence() error = %v", err)
			}
			want := New()
			tt.want(want)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("LoadWithPrecedence() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Zero-value options fall back to the process: HOME, os.Getenv and the cwd.
func TestLoadWithPrecedence_ProcessDefaults(t *testing.T) {
	t.Run("HOME and AGENT_CONFIG", func(t *testing.T) {
		home := t.TempDir()
		writeFile(t, filepath.Join(DefaultConfigDir(home), "agent.toml"), "[agent]\nid = \"from-home\"\n")
		envPath := writeFile(t, filepath.Join(t.TempDir(), "env.toml"), "[agent]\nworkspace = \"/from-env\"\n")
		t.Setenv("HOME", home)
		t.Setenv(EnvConfigPath, envPath)

		got, err := LoadWithPrecedence(LoadOptions{ProjectDir: t.TempDir()})
		if err != nil {
			t.Fatalf("LoadWithPrecedence() error = %v", err)
		}
		if want := (AgentConfig{ID: "from-home", Workspace: "/from-env"}); got.Agent != want {
			t.Errorf("Agent = %+v, want %+v", got.Agent, want)
		}
	})
	t.Run("cwd project file", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "agent.toml"), "[agent]\nid = \"from-cwd\"\n")
		t.Chdir(dir)
		got, err := LoadWithPrecedence(LoadOptions{Home: t.TempDir(), Getenv: func(string) string { return "" }})
		if err != nil {
			t.Fatalf("LoadWithPrecedence() error = %v", err)
		}
		if got.Agent.ID != "from-cwd" {
			t.Errorf("Agent.ID = %q, want %q", got.Agent.ID, "from-cwd")
		}
	})
	t.Run("no home", func(t *testing.T) {
		t.Setenv("HOME", "")
		if _, err := LoadWithPrecedence(LoadOptions{ProjectDir: t.TempDir()}); err == nil {
			t.Error("LoadWithPrecedence() without HOME = nil error, want error")
		}
	})
}

func TestConfig_Profile(t *testing.T) {
	base := LLMConfig{Provider: "anthropic", Model: "sonnet", APIKeyEnv: "ANTHROPIC_API_KEY", MaxTokens: 4096}
	cfg := &Config{
		LLM: base,
		Profiles: map[string]LLMConfig{
			"inherit":  {Model: "opus", BaseURL: "http://proxy", Thinking: "high", MaxRetries: 3, RetryBackoff: "10s"},
			"explicit": {Provider: "openai", Model: "gpt-4o-mini", APIKeyEnv: "OPENAI_API_KEY", MaxTokens: 2048},
		},
	}
	tests := []struct {
		name string
		want LLMConfig
	}{
		{"", base},
		{"nonexistent", base},
		{"inherit", LLMConfig{Provider: "anthropic", Model: "opus", APIKeyEnv: "ANTHROPIC_API_KEY", MaxTokens: 4096, BaseURL: "http://proxy", Thinking: "high", MaxRetries: 3, RetryBackoff: "10s"}},
		{"explicit", LLMConfig{Provider: "openai", Model: "gpt-4o-mini", APIKeyEnv: "OPENAI_API_KEY", MaxTokens: 2048}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, cfg.Profile(tt.name)); diff != "" {
				t.Errorf("Profile(%q) mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}
