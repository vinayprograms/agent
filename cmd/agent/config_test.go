package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinayprograms/agentkit/credentials"
)

// configEnv puts the harness in a scratch working directory with a scratch
// home, so both the cwd and the --default target are writable and empty.
func configEnv(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	t.Chdir(t.TempDir())
	return h
}

func write(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---------------------------------------------------------------------------
// init

func TestConfigInit_WritesBothFilesIntoTheWorkingDirectory(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("config", "init", "--provider", "openai", "--model", "gpt-4o"); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, name := range []string{"agent.toml", "policy.toml"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("%s mode = %v", name, info.Mode().Perm())
		}
		if !strings.Contains(h.out.String(), "wrote "+name) {
			t.Errorf("init must print %s; got %q", name, h.out.String())
		}
	}
	if _, err := os.Stat("credentials.toml"); err == nil {
		t.Error("no credentials file may be written without --api-key")
	}
	body, _ := os.ReadFile("agent.toml")
	if !strings.Contains(string(body), `model = "gpt-4o"`) {
		t.Errorf("agent.toml: %s", body)
	}
}

func TestConfigInit_DefaultTargetsUserConfigDir(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("config", "init", "--default", "--provider", "anthropic"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.deps.home, ".config", "agent", "agent.toml")); err != nil {
		t.Errorf("--default must write to ~/.config/agent: %v", err)
	}
	if !strings.Contains(h.out.String(), "wrote ~/.config/agent/agent.toml") {
		t.Errorf("paths must print home-relative; got %q", h.out.String())
	}
}

func TestConfigInit_DirTargetsThatDirectory(t *testing.T) {
	h := configEnv(t)
	dir := t.TempDir()
	if err := h.exec("config", "init", "--dir", dir, "--provider", "openai"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "policy.toml")); err != nil {
		t.Errorf("--dir must write to the given directory: %v", err)
	}
}

func TestConfigInit_RefusesToOverwriteWithoutForce(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "# mine\n")

	err := h.exec("config", "init", "--provider", "openai")
	if err == nil {
		t.Fatal("init over an existing agent.toml must fail")
	}
	if !strings.Contains(err.Error(), "agent.toml already exists") || !strings.Contains(err.Error(), "--force") {
		t.Errorf("error must name the file and --force: %v", err)
	}
	if body, _ := os.ReadFile("agent.toml"); string(body) != "# mine\n" {
		t.Error("a refused init must not modify anything")
	}
	if _, err := os.Stat("policy.toml"); err == nil {
		t.Error("a refused init must write nothing at all")
	}

	h = configEnv(t)
	t.Chdir(filepath.Dir(mustAbs(t, "agent.toml")))
	if err := h.exec("config", "init", "--provider", "openai", "--force"); err != nil {
		t.Fatalf("--force: %v", err)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestConfigInit_StoresAPIKeyInACredentialsFile(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("config", "init", "--provider", "anthropic", "--api-key", "sk-ant-secret-value"); err != nil {
		t.Fatalf("init: %v", err)
	}
	info, err := os.Stat("credentials.toml")
	if err != nil {
		t.Fatalf("credentials.toml: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credentials.toml mode = %v, want 0600", info.Mode().Perm())
	}
	body, _ := os.ReadFile("credentials.toml")
	if !strings.Contains(string(body), "sk-ant-secret-value") {
		t.Errorf("credentials.toml: %s", body)
	}
	if agent, _ := os.ReadFile("agent.toml"); strings.Contains(string(agent), "api_key") {
		t.Error("the key must never land in agent.toml")
	}
}

func TestConfigInit_APIKeyEnvRecordsTheVariableInsteadOfAFile(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("config", "init", "--provider", "openai", "--api-key-env", "MY_OPENAI_KEY"); err != nil {
		t.Fatalf("init: %v", err)
	}
	body, _ := os.ReadFile("agent.toml")
	if !strings.Contains(string(body), `api_key_env = "MY_OPENAI_KEY"`) {
		t.Errorf("agent.toml: %s", body)
	}
	if _, err := os.Stat("credentials.toml"); err == nil {
		t.Error("--api-key-env must not write a credentials file")
	}
}

func TestConfigInit_ScenarioShapesTheFiles(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("config", "init", "--scenario", "production", "--model", "m"); err != nil {
		t.Fatalf("init: %v", err)
	}
	policy, _ := os.ReadFile("policy.toml")
	if !strings.Contains(string(policy), "default_deny = true") || strings.Contains(string(policy), "[tools.bash]") {
		t.Errorf("production policy: %s", policy)
	}
	agent, _ := os.ReadFile("agent.toml")
	if !strings.Contains(string(agent), `mode = "paranoid"`) || !strings.Contains(string(agent), "[profiles.") {
		t.Errorf("production agent.toml: %s", agent)
	}
}

func TestConfigInit_SmallModelFlag(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("config", "init", "--scenario", "local", "--small-model", "tiny:1b"); err != nil {
		t.Fatalf("init: %v", err)
	}
	body, _ := os.ReadFile("agent.toml")
	if !strings.Contains(string(body), `model = "tiny:1b"`) {
		t.Errorf("agent.toml: %s", body)
	}
}

func TestConfigInit_Errors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"bad provider", []string{"--provider", "nonesuch"}, `unknown provider "nonesuch"`},
		{"bad scenario", []string{"--scenario", "nonesuch"}, `unknown scenario "nonesuch"`},
		{"both targets", []string{"--dir", ".", "--default"}, "mutually exclusive"},
		{"both credentials", []string{"--api-key", "k", "--api-key-env", "V"}, "api-key"},
		{"no default model", []string{"--provider", "custom"}, "pass --model"},
		{"custom needs base url", []string{"--provider", "custom", "--model", "m"}, "needs a base_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := configEnv(t)
			err := h.exec(append([]string{"config", "init"}, tt.args...)...)
			if err == nil {
				t.Fatalf("expected an error, wrote %q", h.out.String())
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// path

func TestConfigPath_ReportsPrecedenceAndState(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "")
	write(t, filepath.Join(h.deps.home, ".config", "agent", "agent.toml"), "")
	write(t, filepath.Join(h.deps.home, ".config", "agent", "policy.toml"), "")
	write(t, "credentials.toml", "")
	write(t, filepath.Join(h.deps.home, ".config", "agent", "credentials.toml"), "")

	if err := h.exec("config", "path"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{
		"~/.config/agent/agent.toml", "~/.config/agent/policy.toml",
		"policy.toml", "~/.agent/credentials.toml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing candidate %q:\n%s", want, out)
		}
	}
	// agent.toml merges, so both existing files are in effect; credentials
	// is first-found-wins, so the user-level file is shadowed.
	if strings.Count(out, "in effect") != 4 {
		t.Errorf("expected 4 files in effect:\n%s", out)
	}
	if !strings.Contains(out, "shadowed") {
		t.Errorf("the losing credentials file must be marked shadowed:\n%s", out)
	}
}

func TestConfigPath_ExplicitTargetHasOneCandidatePerFile(t *testing.T) {
	h := configEnv(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "agent.toml"), "")
	if err := h.exec("config", "path", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if strings.Count(out, dir) != 3 || strings.Contains(out, ".config/agent") {
		t.Errorf("an explicit target must only report its own directory:\n%s", out)
	}
	if strings.Count(out, "missing") != 2 || strings.Count(out, "in effect") != 1 {
		t.Errorf("unexpected states:\n%s", out)
	}
}

func TestConfigPath_HonoursAgentConfigEnvVar(t *testing.T) {
	h := configEnv(t)
	extra := write(t, filepath.Join(t.TempDir(), "extra.toml"), "")
	h.deps.getenv = func(k string) string {
		if k == "AGENT_CONFIG" {
			return extra
		}
		return ""
	}
	if err := h.exec("config", "path"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), extra) {
		t.Errorf("$AGENT_CONFIG must appear as a candidate:\n%s", h.out.String())
	}
}

// ---------------------------------------------------------------------------
// show

func TestConfigShow_HeadsEachFileWithItsSource(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[llm]\nmodel = \"gpt-4o\"\n")
	write(t, filepath.Join(h.deps.home, ".config", "agent", "agent.toml"), "[llm]\nprovider = \"openai\"\n")
	write(t, "policy.toml", "default_deny = true\n")

	if err := h.exec("config", "show"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.Contains(out, "agent.toml: ~/.config/agent/agent.toml + agent.toml (merged)") {
		t.Errorf("merged header missing:\n%s", out)
	}
	if !strings.Contains(out, `model = "gpt-4o"`) || !strings.Contains(out, `provider = "openai"`) {
		t.Errorf("both sources must be printed:\n%s", out)
	}
	if !strings.Contains(out, "policy.toml: policy.toml") {
		t.Errorf("single-source header missing:\n%s", out)
	}
	if !strings.Contains(out, "credentials.toml: (none of") {
		t.Errorf("a missing file must list where it was looked for:\n%s", out)
	}
}

func TestConfigShow_RedactsSecrets(t *testing.T) {
	h := configEnv(t)
	write(t, "credentials.toml", "[anthropic]\napi_key = \"sk-ant-0123456789abcd\"\n[openai]\napi_key = \"short\"\n")

	if err := h.exec("config", "show"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if strings.Contains(out, "sk-ant-0123456789abcd") || strings.Contains(out, "short") {
		t.Errorf("secrets leaked:\n%s", out)
	}
	if !strings.Contains(out, `api_key = "sk-…abcd"`) {
		t.Errorf("long key must keep a recognisable stub:\n%s", out)
	}
	if !strings.Contains(out, `api_key = "***"`) {
		t.Errorf("short key must be fully hidden:\n%s", out)
	}
}

func TestConfigShow_Resolved(t *testing.T) {
	h := configEnv(t)
	write(t, filepath.Join(h.deps.home, ".config", "agent", "agent.toml"), "[llm]\nprovider = \"openai\"\n")
	write(t, "agent.toml", "[llm]\nmodel = \"gpt-4o\"\n[embedding]\napi_key = \"sk-embed-0123456789\"\n")

	if err := h.exec("config", "show", "--resolved"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.Contains(out, `provider = "openai"`) || !strings.Contains(out, `model = "gpt-4o"`) {
		t.Errorf("--resolved must show the merge of both layers:\n%s", out)
	}
	if strings.Contains(out, "sk-embed-0123456789") {
		t.Errorf("--resolved leaked a secret:\n%s", out)
	}
}

func TestConfigShow_ExplicitTargetIgnoresPrecedence(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[llm]\nmodel = \"cwd-model\"\n")
	dir := t.TempDir()
	write(t, filepath.Join(dir, "agent.toml"), "[llm]\nmodel = \"dir-model\"\n")

	if err := h.exec("config", "show", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "cwd-model") {
		t.Errorf("--dir must not read the working directory:\n%s", h.out.String())
	}
	h.out.Reset()
	if err := h.exec("config", "show", "--dir", dir, "--resolved"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "dir-model") {
		t.Errorf("--resolved on an explicit target:\n%s", h.out.String())
	}
}

func TestConfigShow_Errors(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("config", "show", "--dir", ".", "--default"); err == nil {
		t.Error("both target flags must be rejected")
	}

	h = configEnv(t)
	write(t, "agent.toml", "this is not toml")
	if err := h.exec("config", "show", "--resolved"); err == nil {
		t.Error("--resolved must surface a parse error")
	}
}

// ---------------------------------------------------------------------------
// validate

func TestConfigValidate_CleanConfiguration(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("config", "init", "--scenario", "local"); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	if err := h.exec("config", "validate"); err != nil {
		t.Fatalf("a freshly initialised local configuration must validate: %v", err)
	}
	if !strings.Contains(h.out.String(), "✓") {
		t.Errorf("validate output: %q", h.out.String())
	}
	if strings.Contains(h.out.String(), "warning") {
		t.Errorf("a local provider needs no credential: %q", h.out.String())
	}
}

func TestConfigValidate_ReportsEveryProblemAtOnce(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[llm\n")
	write(t, "policy.toml", "[tools.bash]\nenabled = true\n")
	write(t, "credentials.toml", "[anthropic]\napi_key = \"k\"\n")
	if err := os.Chmod("credentials.toml", 0o644); err != nil {
		t.Fatal(err)
	}

	err := h.exec("config", "validate")
	if err == nil {
		t.Fatal("a broken configuration must exit non-zero")
	}
	msg := err.Error()
	for _, want := range []string{"agent.toml", "policy.toml", "credentials.toml", "insecure permissions"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error must mention %q:\n%s", want, msg)
		}
	}
	if !strings.Contains(msg, "3 configuration problem(s)") {
		t.Errorf("all three files must be reported:\n%s", msg)
	}
}

func TestConfigValidate_LegacyPolicyNamesTheReplacements(t *testing.T) {
	h := configEnv(t)
	write(t, "policy.toml", "[tools.web_fetch]\nallow_domains = [\"github.com\"]\n[mcp]\nallowed_tools = [\"a:*\"]\n")

	err := h.exec("config", "validate")
	if err == nil {
		t.Fatal("a legacy policy must fail validation")
	}
	for _, want := range []string{"allow_domains -> allow", "mcp.allowed_tools -> mcp.allow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing replacement %q:\n%s", want, err)
		}
	}
}

func TestConfigValidate_DeprecatedStorageSection(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[storage]\nlocation = \"/tmp/x\"\n")
	err := h.exec("config", "validate")
	if err == nil || !strings.Contains(err.Error(), "[storage] is deprecated") {
		t.Errorf("deprecated section must be reported: %v", err)
	}
}

func TestConfigValidate_MissingCredentialIsAWarningNotAFailure(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[llm]\nprovider = \"anthropic\"\nmodel = \"claude\"\n")

	if err := h.exec("config", "validate"); err != nil {
		t.Fatalf("a missing credential must not fail validation: %v", err)
	}
	if !strings.Contains(h.out.String(), `provider "anthropic" is configured`) {
		t.Errorf("missing credential must be warned about:\n%s", h.out.String())
	}
}

func TestConfigValidate_CredentialFoundSilencesTheWarning(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[llm]\nprovider = \"anthropic\"\nmodel = \"claude\"\n")
	h.deps.credentials = func(string) (credentials.Lookup, error) {
		return credentials.FileStore{"anthropic": {APIKey: "k"}}, nil
	}
	if err := h.exec("config", "validate"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "warning") {
		t.Errorf("a resolvable credential must not warn:\n%s", h.out.String())
	}
}

func TestConfigValidate_CustomAPIKeyEnvSilencesTheWarning(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[llm]\nprovider = \"openai\"\nmodel = \"gpt-4o\"\napi_key_env = \"MY_KEY\"\n")
	h.deps.getenv = func(k string) string {
		if k == "MY_KEY" {
			return "value"
		}
		return ""
	}
	if err := h.exec("config", "validate"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "warning") {
		t.Errorf("api_key_env must satisfy the credential check:\n%s", h.out.String())
	}
}

func TestConfigValidate_UnreadableCredentialsAreReported(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[llm]\nprovider = \"anthropic\"\n")
	h.deps.credentials = func(string) (credentials.Lookup, error) {
		return nil, os.ErrPermission
	}
	if err := h.exec("config", "validate"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "could not read credentials") {
		t.Errorf("credential lookup failure must be surfaced:\n%s", h.out.String())
	}
}

func TestConfigValidate_ExplicitTargetUsesItsOwnCredentialsFile(t *testing.T) {
	h := configEnv(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "agent.toml"), "[llm]\nprovider = \"anthropic\"\n")
	creds := write(t, filepath.Join(dir, "credentials.toml"), "[anthropic]\napi_key = \"k\"\n")
	if err := os.Chmod(creds, 0o600); err != nil {
		t.Fatal(err)
	}
	var gotOverride string
	h.deps.credentials = func(override string) (credentials.Lookup, error) {
		gotOverride = override
		return credentials.FileStore{"anthropic": {APIKey: "k"}}, nil
	}
	if err := h.exec("config", "validate", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if gotOverride != creds {
		t.Errorf("credential override = %q, want %q", gotOverride, creds)
	}
}

func TestConfigValidate_NoDefaultHome(t *testing.T) {
	h := configEnv(t)
	h.deps.home = ""
	if err := h.exec("config", "validate", "--default"); err == nil ||
		!strings.Contains(err.Error(), "home directory") {
		t.Errorf("--default without a home must fail clearly: %v", err)
	}
}

func TestConfigValidate_MalformedPolicyIsReported(t *testing.T) {
	h := configEnv(t)
	write(t, "policy.toml", "default_deny = \n")
	if err := h.exec("config", "validate"); err == nil {
		t.Error("a malformed policy must fail validation")
	}
}

// ---------------------------------------------------------------------------
// setup flags

func TestSetup_TargetFlagsAreRejectedTogether(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("setup", "--dir", ".", "--default"); err == nil ||
		!strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("setup must reject both target flags: %v", err)
	}
}

// ---------------------------------------------------------------------------
// verifier findings

func TestConfigInit_RefusesToOverwriteCredentials(t *testing.T) {
	h := configEnv(t)
	existing := write(t, "credentials.toml", "[anthropic]\napi_key = \"original\"\n")
	if err := os.Chmod(existing, 0o600); err != nil {
		t.Fatal(err)
	}

	err := h.exec("config", "init", "--provider", "anthropic", "--api-key", "new-key")
	if err == nil {
		t.Fatal("init must refuse to overwrite an existing credentials.toml")
	}
	if !strings.Contains(err.Error(), "credentials.toml already exists") || !strings.Contains(err.Error(), "--force") {
		t.Errorf("error must name the file and --force: %v", err)
	}
	if body, _ := os.ReadFile("credentials.toml"); !strings.Contains(string(body), "original") {
		t.Errorf("the existing key must survive a refused init: %s", body)
	}
	// The whole write is refused up front: the other two files are untouched.
	for _, name := range []string{"agent.toml", "policy.toml"} {
		if _, err := os.Stat(name); err == nil {
			t.Errorf("%s must not be written when credentials.toml is in the way", name)
		}
	}
}

func TestConfigInit_ForceOverwritesCredentials(t *testing.T) {
	h := configEnv(t)
	existing := write(t, "credentials.toml", "[openai]\napi_key = \"kept-for-other-provider\"\n")
	if err := os.Chmod(existing, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.exec("config", "init", "--provider", "anthropic", "--api-key", "new-key", "--force"); err != nil {
		t.Fatalf("--force: %v", err)
	}
	body, _ := os.ReadFile("credentials.toml")
	if !strings.Contains(string(body), "new-key") || !strings.Contains(string(body), "kept-for-other-provider") {
		t.Errorf("--force must add the key while preserving other providers: %s", body)
	}
}

func TestConfigShow_RedactsNestedAndUnconventionalSecrets(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", `[llm]
model = "gpt-4o"

[telemetry.headers]
x-honeycomb-team = "hc-0123456789abcdef"
x-tenant = 'single-quoted-secret-value'

[mcp.servers.gh]
command = "gh-mcp"

[mcp.servers.gh.env]
GITHUB_TOKEN = "ghp_0123456789abcdef"
GH_HOST = "github.example.com"

[embedding]
api_key = 'sk-single-0123456789'

[service]
authorization = "Bearer 0123456789abcdef"
`)
	if err := h.exec("config", "show"); err != nil {
		t.Fatalf("show: %v", err)
	}
	out := h.out.String()
	for _, secret := range []string{
		"hc-0123456789abcdef",        // telemetry header value, unconventional key
		"single-quoted-secret-value", // single-quoted header value
		"ghp_0123456789abcdef",       // MCP server env value
		"github.example.com",         // every env value is opaque, not just the token
		"sk-single-0123456789",       // single-quoted api_key
		"Bearer 0123456789abcdef",    // authorization
	} {
		if strings.Contains(out, secret) {
			t.Errorf("leaked %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, `model = "gpt-4o"`) || !strings.Contains(out, "gh-mcp") {
		t.Errorf("non-secret values must survive:\n%s", out)
	}
	if !strings.Contains(out, "cdef") {
		t.Errorf("the mask must keep the last four characters:\n%s", out)
	}
}

func TestConfigShow_ResolvedRedactsNestedSecrets(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[telemetry]\nenabled = true\n[telemetry.headers]\nx-key = \"hc-0123456789abcdef\"\n")
	if err := h.exec("config", "show", "--resolved"); err != nil {
		t.Fatalf("show --resolved: %v", err)
	}
	if strings.Contains(h.out.String(), "hc-0123456789abcdef") {
		t.Errorf("--resolved leaked a telemetry header:\n%s", h.out.String())
	}
}

func TestConfigShow_UnparseableFileIsAnErrorNotARawDump(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "api_key = \"sk-0123456789abcdef\"\nthis is not toml\n")
	err := h.exec("config", "show")
	if err == nil {
		t.Fatal("an unparseable file must not be printed")
	}
	if strings.Contains(h.out.String(), "sk-0123456789abcdef") {
		t.Errorf("a file that cannot be parsed cannot be redacted, so it must not be shown:\n%s", h.out.String())
	}
}

func TestConfigTarget_ExpandsTilde(t *testing.T) {
	h := configEnv(t)
	if err := h.exec("config", "init", "--dir", "~/nested/cfg", "--provider", "openai"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.deps.home, "nested", "cfg", "agent.toml")); err != nil {
		t.Errorf("--dir must expand ~: %v", err)
	}
}

func TestConfigValidate_DoesNotDoubleTheFileName(t *testing.T) {
	h := configEnv(t)
	write(t, "agent.toml", "[limits]\nmax_duration = \"not-a-duration\"\n")
	err := h.exec("config", "validate")
	if err == nil {
		t.Fatal("an invalid duration must fail validation")
	}
	if strings.Contains(err.Error(), "agent.toml: agent.toml") {
		t.Errorf("the file must be named once:\n%s", err)
	}
	if !strings.Contains(err.Error(), "agent.toml") {
		t.Errorf("the file must still be named:\n%s", err)
	}
}
