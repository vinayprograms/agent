package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/run"
	"github.com/vinayprograms/agentkit/credentials"
)

// fakeRuntime stands in for the agent runtime so the command layer can be
// executed without an LLM.
type fakeRuntime struct {
	err    error
	ran    bool
	closed bool
}

func (f *fakeRuntime) Run(context.Context) error { f.ran = true; return f.err }
func (f *fakeRuntime) Close()                    { f.closed = true }

// harness executes the root command in-process and captures its streams.
type harness struct {
	deps    deps
	last    *run.Loaded
	lastRun run.Deps
	rt      *fakeRuntime
	out     strings.Builder
	err     strings.Builder
}

// newHarness returns a harness whose credentials and runtime are fakes and
// whose home is a temp dir.
func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{rt: &fakeRuntime{}}
	h.deps = deps{
		home:        t.TempDir(),
		getenv:      func(string) string { return "" },
		credentials: func(string) (credentials.Lookup, error) { return credentials.NewEnvStore(), nil },
		isTerminal:  func(any) bool { return false },
		newRuntime: func(_ context.Context, l *run.Loaded, d run.Deps) (runner, error) {
			h.last, h.lastRun = l, d
			return h.rt, nil
		},
	}
	return h
}

// exec runs the root command with args and returns its error.
func (h *harness) exec(args ...string) error {
	root := newRootCmd(h.deps)
	root.SetArgs(args)
	root.SetOut(&h.out)
	root.SetErr(&h.err)
	return root.ExecuteContext(context.Background())
}

func writeAgentfile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Agentfile")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validAgentfile = `NAME test
INPUT topic DEFAULT "golang"
GOAL analyze "Analyze $topic"
RUN main USING analyze
`

func TestRoot_HelpAndVersion(t *testing.T) {
	h := newHarness(t)
	if err := h.exec("--help"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "Usage:") || !strings.Contains(h.out.String(), "run") {
		t.Errorf("help output: %q", h.out.String())
	}

	h = newHarness(t)
	if err := h.exec("version"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "agent version") {
		t.Errorf("version output: %q", h.out.String())
	}
}

// TestRoot_CommandSurface pins every command and flag documented in
// docs/usage/cli-reference.md and used by the Makefile.
func TestRoot_CommandSurface(t *testing.T) {
	root := newRootCmd(newHarness(t).deps)
	want := map[string][]string{
		"run":      {"input", "config", "policy", "credentials", "workspace", "goal", "debug", "step", "session-label"},
		"serve":    {"config", "policy", "credentials", "workspace", "state", "http", "bus", "queue-group", "capability", "session-label", "type", "capabilities"},
		"validate": nil,
		"inspect":  nil,
		"pack":     {"output", "sign", "author", "email", "license"},
		"verify":   {"key"},
		"install":  {"target", "key", "no-deps", "dry-run"},
		"keygen":   {"output"},
		"config":   nil,
		"setup":    {"dir", "default"},
		"replay":   {"verbose", "no-pager", "cost", "list", "last", "name", "agentfile", "label", "status", "since", "state", "config"},
		"version":  nil,
	}
	if len(root.Commands()) != len(want) {
		t.Errorf("command count = %d, want %d", len(root.Commands()), len(want))
	}
	for name, flags := range want {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Errorf("missing command %q: %v", name, err)
			continue
		}
		for _, f := range flags {
			if cmd.Flags().Lookup(f) == nil {
				t.Errorf("%s: missing flag --%s", name, f)
			}
		}
	}
	// Short flags and defaults the docs promise.
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd.Flags().ShorthandLookup("i") == nil {
		t.Error("run: missing -i shorthand")
	}
	keygen, _, _ := root.Find([]string{"keygen"})
	if got := keygen.Flags().Lookup("output"); got == nil || got.DefValue != "agent-key" || keygen.Flags().ShorthandLookup("o") == nil {
		t.Errorf("keygen --output: %v", got)
	}
	// config's own subcommands and their flags.
	configWant := map[string][]string{
		"init":     {"dir", "default", "provider", "model", "small-model", "scenario", "api-key", "api-key-env", "force"},
		"show":     {"dir", "default", "resolved"},
		"validate": {"dir", "default"},
		"path":     {"dir", "default"},
	}
	cfg, _, _ := root.Find([]string{"config"})
	if len(cfg.Commands()) != len(configWant) {
		t.Errorf("config subcommand count = %d, want %d", len(cfg.Commands()), len(configWant))
	}
	for name, flags := range configWant {
		sub, _, err := cfg.Find([]string{name})
		if err != nil || sub.Name() != name {
			t.Errorf("missing command %q: %v", name, err)
			continue
		}
		for _, f := range flags {
			if sub.Flags().Lookup(f) == nil {
				t.Errorf("config %s: missing flag --%s", name, f)
			}
		}
	}

	pack, _, _ := root.Find([]string{"pack"})
	if pack.Flags().ShorthandLookup("o") == nil {
		t.Error("pack: missing -o shorthand")
	}
}

func TestValidate(t *testing.T) {
	h := newHarness(t)
	if err := h.exec("validate", writeAgentfile(t, validAgentfile)); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !strings.Contains(h.out.String(), "✓ Valid") {
		t.Errorf("output %q", h.out.String())
	}

	// Invalid Agentfile and missing file both fail.
	h = newHarness(t)
	if err := h.exec("validate", writeAgentfile(t, "GOAL g \"x\"\nRUN main USING g\n")); err == nil {
		t.Error("expected validation failure")
	}
	h = newHarness(t)
	if err := h.exec("validate", filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected not-found error")
	}
}

func TestValidate_DefaultAgentfile(t *testing.T) {
	path := writeAgentfile(t, validAgentfile)
	t.Chdir(filepath.Dir(path))
	h := newHarness(t)
	if err := h.exec("validate"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !strings.Contains(h.out.String(), "✓ Valid") {
		t.Errorf("output %q", h.out.String())
	}
}

func TestInspect(t *testing.T) {
	h := newHarness(t)
	if err := h.exec("inspect", writeAgentfile(t, validAgentfile)); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	h = newHarness(t)
	if err := h.exec("inspect", filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected not-found error")
	}
}

func TestRun_LoadsAndRuns(t *testing.T) {
	path := writeAgentfile(t, validAgentfile)
	h := newHarness(t)
	err := h.exec("run", path,
		"-i", "key=value", "-i", "foo=bar",
		"--config", filepath.Join(t.TempDir(), "missing.toml"))
	if err == nil {
		t.Fatal("expected config error")
	}

	h = newHarness(t)
	if err := h.exec("run", path, "-i", "topic=go", "--debug", "--workspace", t.TempDir()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !h.rt.ran || !h.rt.closed {
		t.Errorf("runtime not driven: %+v", h.rt)
	}
	if h.last.Workflow.Name != "test" || h.last.Inputs["topic"] != "go" || !h.last.Debug {
		t.Errorf("loaded = %+v", h.last)
	}
	if h.lastRun.Version != version || h.lastRun.Creds == nil {
		t.Errorf("run deps = %+v", h.lastRun)
	}
}

// TestRun_SessionLabel pins that --session-label reaches the loaded
// workflow, and that a run without it records no label.
func TestRun_SessionLabel(t *testing.T) {
	path := writeAgentfile(t, validAgentfile)
	h := newHarness(t)
	if err := h.exec("run", path, "--workspace", t.TempDir()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if h.last.SessionLabel != "" {
		t.Errorf("SessionLabel = %q, want empty without the flag", h.last.SessionLabel)
	}
	h = newHarness(t)
	if err := h.exec("run", path, "--session-label", "nightly", "--workspace", t.TempDir()); err != nil {
		t.Fatalf("run --session-label: %v", err)
	}
	if h.last.SessionLabel != "nightly" {
		t.Errorf("SessionLabel = %q, want %q", h.last.SessionLabel, "nightly")
	}
}

func TestRun_InlineGoalSkipsAgentfile(t *testing.T) {
	h := newHarness(t)
	if err := h.exec("run", "--goal", "summarise the news", "--workspace", t.TempDir()); err != nil {
		t.Fatalf("run --goal: %v", err)
	}
	if h.last.Workflow.Goals[0].Outcome != "summarise the news" {
		t.Errorf("loaded = %+v", h.last.Workflow)
	}
}

// TestRun_ClosesRuntimeOnFailure pins the bug that os.Exit used to cause:
// a failing run must still close the runtime, and must exit non-zero.
func TestRun_ClosesRuntimeOnFailure(t *testing.T) {
	h := newHarness(t)
	h.rt.err = errors.New("workflow blew up")
	err := h.exec("run", writeAgentfile(t, validAgentfile), "--workspace", t.TempDir())
	if err == nil {
		t.Fatal("expected the run error")
	}
	if !h.rt.closed {
		t.Error("runtime must be closed even when Run fails")
	}
}

func TestRun_MissingAgentfileAndCredentials(t *testing.T) {
	h := newHarness(t)
	if err := h.exec("run", filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected missing Agentfile error")
	}

	h = newHarness(t)
	h.deps.credentials = func(string) (credentials.Lookup, error) { return nil, errors.New("bad credentials") }
	if err := h.exec("run"); err == nil {
		t.Error("expected credentials error")
	}

	h = newHarness(t)
	h.deps.newRuntime = func(context.Context, *run.Loaded, run.Deps) (runner, error) {
		return nil, errors.New("no runtime")
	}
	if err := h.exec("run", writeAgentfile(t, validAgentfile), "--workspace", t.TempDir()); err == nil {
		t.Error("expected runtime construction error")
	}
}

// TestRun_CredentialsFlagPassesOverride pins that --credentials reaches
// d.credentials as the override path, for both run and serve.
func TestRun_CredentialsFlagPassesOverride(t *testing.T) {
	h := newHarness(t)
	var gotOverride string
	h.deps.credentials = func(override string) (credentials.Lookup, error) {
		gotOverride = override
		return credentials.NewEnvStore(), nil
	}
	if err := h.exec("run", writeAgentfile(t, validAgentfile), "--workspace", t.TempDir(), "--credentials", "/some/creds.toml"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotOverride != "/some/creds.toml" {
		t.Errorf("credentials override = %q, want /some/creds.toml", gotOverride)
	}
}

func TestServe_NoTransportConfigured(t *testing.T) {
	h := newHarness(t)
	h.deps.credentials = func(string) (credentials.Lookup, error) { return nil, errors.New("bad credentials") }
	if err := h.exec("serve", writeAgentfile(t, validAgentfile), "--workspace", t.TempDir()); err == nil {
		t.Error("expected credentials error")
	}

	h = newHarness(t)
	if err := h.exec("serve", filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected missing Agentfile error")
	}
}

// TestServe_CredentialsFlagPassesOverride mirrors
// TestRun_CredentialsFlagPassesOverride for `agent serve`.
func TestServe_CredentialsFlagPassesOverride(t *testing.T) {
	h := newHarness(t)
	var gotOverride string
	h.deps.credentials = func(override string) (credentials.Lookup, error) {
		gotOverride = override
		return credentials.NewEnvStore(), nil
	}
	// No transport configured, so this still errors out after loading
	// credentials — enough to observe the override that reached deps.
	_ = h.exec("serve", writeAgentfile(t, validAgentfile), "--workspace", t.TempDir(), "--credentials", "/some/creds.toml")
	if gotOverride != "/some/creds.toml" {
		t.Errorf("credentials override = %q, want /some/creds.toml", gotOverride)
	}
}

// TestLoadCredentials_MissingOverrideNamesPath pins that loadCredentials
// surfaces a missing --credentials file as an error naming the path,
// rather than silently falling back to the standard locations.
func TestLoadCredentials_MissingOverrideNamesPath(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope.toml")
	_, err := loadCredentials(home, missing)
	if err == nil {
		t.Fatal("expected an error for a missing --credentials file")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error should name %q, got %v", missing, err)
	}
}

func TestLoadCredentials_ExistingOverrideLoads(t *testing.T) {
	home := t.TempDir()
	override := filepath.Join(t.TempDir(), "creds.toml")
	if err := os.WriteFile(override, []byte("[anthropic]\napi_key = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCredentials(home, override); err != nil {
		t.Fatalf("loadCredentials: %v", err)
	}
}

func TestKeygenAndPackFlags(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t)
	if err := h.exec("keygen", "-o", filepath.Join(dir, "custom-key")); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "custom-key.pem")); err != nil {
		t.Errorf("private key: %v", err)
	}
	// A second keygen on the same prefix refuses to overwrite.
	h = newHarness(t)
	if err := h.exec("keygen", "-o", filepath.Join(dir, "custom-key")); err == nil {
		t.Error("expected refusal to overwrite")
	}
}

func TestCheckKeyPaths(t *testing.T) {
	dir := t.TempDir()
	priv, pub := filepath.Join(dir, "k.pem"), filepath.Join(dir, "k.pub")
	if err := checkKeyPaths(priv, pub); err != nil {
		t.Errorf("fresh paths: %v", err)
	}
	if err := os.WriteFile(priv, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkKeyPaths(priv, pub); err == nil {
		t.Error("existing private key should be refused")
	}
	if err := os.Remove(priv); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pub, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkKeyPaths(priv, pub); err == nil {
		t.Error("existing public key should be refused")
	}
}

func TestArgOr(t *testing.T) {
	if got := argOr(nil, "Agentfile"); got != "Agentfile" {
		t.Errorf("got %q", got)
	}
	if got := argOr([]string{"x"}, "Agentfile"); got != "x" {
		t.Errorf("got %q", got)
	}
}

func TestNewDeps(t *testing.T) {
	d := newDeps()
	if d.getenv == nil || d.credentials == nil || d.newRuntime == nil {
		t.Fatal("newDeps left a hole")
	}
	if got := d.getenv("PATH"); got == "" {
		t.Error("getenv should read the process environment")
	}
}

// TestPackageCommands drives pack, verify, install and inspect through the
// real root command so the routing layer, not just the helpers, is covered.
func TestPackageCommands(t *testing.T) {
	src := filepath.Dir(writeAgentfile(t, validAgentfile))
	keys := filepath.Join(t.TempDir(), "k")
	out := filepath.Join(t.TempDir(), "pkg.agent")
	target := t.TempDir()

	h := newHarness(t)
	if err := h.exec("keygen", "-o", keys); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	h = newHarness(t)
	err := h.exec("pack", src, "-o", out,
		"--sign", keys+".pem", "--author", "Test Author", "--email", "t@example.com", "--license", "MIT")
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	h = newHarness(t)
	if err := h.exec("verify", out, "--key", keys+".pub"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	h = newHarness(t)
	if err := h.exec("install", out, "--target", target, "--key", keys+".pub", "--dry-run"); err != nil {
		t.Fatalf("install --dry-run: %v", err)
	}
	h = newHarness(t)
	if err := h.exec("install", out, "--target", target, "--no-deps"); err != nil {
		t.Fatalf("install: %v", err)
	}
	h = newHarness(t)
	if err := h.exec("inspect", out); err != nil {
		t.Fatalf("inspect package: %v", err)
	}

	// Failure paths route errors back out of the command.
	for _, args := range [][]string{
		{"pack", t.TempDir()},
		{"verify", filepath.Join(t.TempDir(), "nope.agent")},
		{"install", filepath.Join(t.TempDir(), "nope.agent"), "--target", target},
	} {
		h := newHarness(t)
		if err := h.exec(args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

// syncBuffer is a writer safe to read while the command under test writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestServe_HTTPModeStartsAndDrains runs the serve command end to end on a
// loopback port and shuts it down through the command's context.
func TestServe_HTTPModeStartsAndDrains(t *testing.T) {
	dir := filepath.Dir(writeAgentfile(t, validAgentfile))
	cfg := "[agent]\nworkspace = \"" + dir + "\"\n" +
		"[state]\nlocation = \"" + filepath.Join(dir, "state") + "\"\n" +
		"[llm]\nprovider = \"ollama-local\"\nmodel = \"llama3\"\n"
	cfgPath := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t)
	var banner syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	root := newRootCmd(h.deps)
	root.SetArgs([]string{"serve", filepath.Join(dir, "Agentfile"),
		"--config", cfgPath, "--http", "127.0.0.1:0", "--session-label", "svc", "--capability", "cap"})
	root.SetOut(&h.out)
	root.SetErr(&banner)

	errc := make(chan error, 1)
	go func() { errc <- root.ExecuteContext(ctx) }()

	// The listener is up once the banner has been written.
	deadline := time.After(10 * time.Second)
	for !strings.Contains(banner.String(), "HTTP server listening") {
		select {
		case err := <-errc:
			t.Fatalf("serve exited early: %v (stderr: %s)", err, banner.String())
		case <-deadline:
			t.Fatalf("serve never started: %s", banner.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	if err := <-errc; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestServe_NoTransport(t *testing.T) {
	dir := filepath.Dir(writeAgentfile(t, validAgentfile))
	cfgPath := filepath.Join(dir, "agent.toml")
	cfg := "[agent]\nworkspace = \"" + dir + "\"\n" +
		"[state]\nlocation = \"" + filepath.Join(dir, "state") + "\"\n" +
		"[llm]\nprovider = \"ollama-local\"\nmodel = \"llama3\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	err := h.exec("serve", filepath.Join(dir, "Agentfile"), "--config", cfgPath)
	if err == nil || !strings.Contains(err.Error(), "no transport configured") {
		t.Errorf("expected the no-transport error, got %v", err)
	}
}
