package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agent/internal/configfile"
	"github.com/vinayprograms/agent/internal/run"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/policy"
)

// newConfigCmd groups the non-interactive counterparts of `agent setup`:
// create, inspect and check the three configuration files.
func newConfigCmd(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Create and inspect agent configuration files",
		Long: "Create and inspect the three configuration files agent reads:\n" +
			"agent.toml (models and features), policy.toml (what tools may do)\n" +
			"and credentials.toml (API keys).\n\n" +
			"Every subcommand takes the same target: --dir points at a directory,\n" +
			"--default at ~/.config/agent, and neither means the current directory\n" +
			"with the normal lookup precedence applied.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newConfigInitCmd(d),
		newConfigShowCmd(d),
		newConfigValidateCmd(d),
		newConfigPathCmd(d),
	)
	return cmd
}

// ---------------------------------------------------------------------------
// target — which files the subcommands operate on

// target is the location a `agent config` subcommand reads or writes. An
// explicit target (--dir/--default) is a single directory; the default target
// is the working directory, where the full lookup precedence applies and a
// file can therefore have several sources.
type target struct {
	dir      string
	explicit bool
	home     string
	getenv   func(string) string
}

// targetFlags are the --dir/--default pair every subcommand carries.
type targetFlags struct {
	dir     string
	useHome bool
}

// bind attaches the target flags to a command.
func (t *targetFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&t.dir, "dir", "", "Directory holding the configuration files")
	cmd.Flags().BoolVar(&t.useHome, "default", false, "Use the user-level directory (~/.config/agent)")
}

// resolve turns the flags into a target. Neither flag means the working
// directory with the standard precedence.
func (t targetFlags) resolve(d deps) (target, error) {
	tg := target{home: d.home, getenv: d.getenv}
	switch {
	case t.dir != "" && t.useHome:
		return tg, errors.New("--dir and --default are mutually exclusive")
	case t.useHome:
		if d.home == "" {
			return tg, errors.New("--default needs a home directory, which could not be resolved")
		}
		tg.dir, tg.explicit = config.DefaultConfigDir(d.home), true
	case t.dir != "":
		tg.dir, tg.explicit = expandHome(t.dir, d.home), true
	default:
		tg.dir = "."
	}
	return tg, nil
}

// expandHome replaces a leading ~ in p with home, so --dir ~/x works even
// when the shell did not expand it (quoted, or from a config file).
func expandHome(p, home string) string {
	if home == "" || p != "~" && !strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// fileset is one configuration file at a target: the candidate paths in
// precedence order and how they combine. agent.toml is merged — every file
// that exists contributes — while policy.toml and credentials.toml are
// first-found-wins.
type fileset struct {
	name       string
	candidates []string
	merged     bool
}

// filesets returns the three configuration files in the order the commands
// report them.
func (t target) filesets() []fileset {
	if t.explicit {
		var fs []fileset
		for _, name := range []string{"agent.toml", "policy.toml", "credentials.toml"} {
			fs = append(fs, fileset{name: name, candidates: []string{filepath.Join(t.dir, name)}})
		}
		fs[0].merged = true
		return fs
	}

	userDir := config.DefaultConfigDir(t.home)
	agent := []string{filepath.Join(userDir, "agent.toml"), "agent.toml"}
	if env := t.getenv(config.EnvConfigPath); env != "" {
		agent = append(agent, env)
	}
	creds, _ := run.CredentialPaths(t.home, "")
	return []fileset{
		{name: "agent.toml", candidates: agent, merged: true},
		{name: "policy.toml", candidates: []string{"policy.toml", filepath.Join(userDir, "policy.toml")}},
		{name: "credentials.toml", candidates: creds},
	}
}

// active returns the candidates that exist and actually take effect: all of
// them for a merged file, only the first for a first-found-wins file.
func (f fileset) active() []string {
	var found []string
	for _, p := range f.candidates {
		if fileExists(p) {
			found = append(found, p)
			if !f.merged {
				break
			}
		}
	}
	return found
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// display shortens a path under the home directory to ~/… for output.
func display(path, home string) string {
	if home == "" || !strings.HasPrefix(path, home+string(filepath.Separator)) {
		return path
	}
	return "~" + strings.TrimPrefix(path, home)
}

// loadConfig loads the effective agent.toml for the target.
func (t target) loadConfig() (*config.Config, error) {
	if !t.explicit {
		return config.LoadWithPrecedence(config.LoadOptions{Home: t.home, Getenv: t.getenv})
	}
	path := filepath.Join(t.dir, "agent.toml")
	if !fileExists(path) {
		return config.New(), nil
	}
	return config.LoadFile(path)
}

// ---------------------------------------------------------------------------
// init

// initOptions are the `config init` flags.
type initOptions struct {
	target     targetFlags
	provider   string
	model      string
	smallModel string
	scenario   string
	apiKey     string
	apiKeyEnv  string
	force      bool
}

func newConfigInitCmd(d deps) *cobra.Command {
	var opts initOptions
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter agent.toml and policy.toml",
		Long: "Write a starter agent.toml and policy.toml — the same files `agent setup`\n" +
			"produces, without the questions. Existing files are never overwritten\n" +
			"unless --force is given.\n\n" +
			"--api-key stores the key in a credentials.toml (mode 0600) beside them;\n" +
			"--api-key-env instead records the environment variable to read it from,\n" +
			"and writes no credentials file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigInit(cmd.OutOrStdout(), d, opts)
		},
	}
	opts.target.bind(cmd)
	f := cmd.Flags()
	f.StringVar(&opts.provider, "provider", "", "LLM provider ("+strings.Join(configfile.Providers(), ", ")+")")
	f.StringVar(&opts.model, "model", "", "Main model name (default: the provider's recommended model)")
	f.StringVar(&opts.smallModel, "small-model", "", "Fast model for summarization and triage")
	f.StringVar(&opts.scenario, "scenario", configfile.ScenarioDev, "Deployment scenario ("+strings.Join(configfile.Scenarios(), ", ")+")")
	f.StringVar(&opts.apiKey, "api-key", "", "API key to store in credentials.toml")
	f.StringVar(&opts.apiKeyEnv, "api-key-env", "", "Environment variable the API key is read from")
	f.BoolVar(&opts.force, "force", false, "Overwrite existing configuration files")
	cmd.MarkFlagsMutuallyExclusive("api-key", "api-key-env")
	return cmd
}

func runConfigInit(out io.Writer, d deps, opts initOptions) error {
	tg, err := opts.target.resolve(d)
	if err != nil {
		return err
	}
	o, err := initOptionsToConfig(opts)
	if err != nil {
		return err
	}

	// Everything that would be overwritten is checked before anything is
	// written, so a refused init leaves all three files untouched.
	credsPath := filepath.Join(tg.dir, "credentials.toml")
	if !opts.force {
		guarded := []string{filepath.Join(tg.dir, "agent.toml"), filepath.Join(tg.dir, "policy.toml")}
		if opts.apiKey != "" {
			guarded = append(guarded, credsPath)
		}
		if err := configfile.EnsureAbsent(guarded...); err != nil {
			return err
		}
	}

	written, err := configfile.Write(tg.dir, o, true)
	if err != nil {
		return err
	}
	if opts.apiKey != "" {
		path, err := writeAPIKey(credsPath, o.Provider, opts.apiKey)
		if err != nil {
			return err
		}
		written = append(written, path)
	}
	for _, p := range written {
		fmt.Fprintf(out, "wrote %s\n", display(p, d.home))
	}
	return nil
}

// initOptionsToConfig turns the flags into the configuration to generate:
// scenario defaults first, then the explicit overrides on top.
func initOptionsToConfig(opts initOptions) (configfile.Options, error) {
	if !slices.Contains(configfile.Scenarios(), opts.scenario) {
		return configfile.Options{}, fmt.Errorf("unknown scenario %q (choose one of %s)",
			opts.scenario, strings.Join(configfile.Scenarios(), ", "))
	}
	if opts.provider != "" && !slices.Contains(configfile.Providers(), opts.provider) {
		return configfile.Options{}, fmt.Errorf("unknown provider %q (choose one of %s)",
			opts.provider, strings.Join(configfile.Providers(), ", "))
	}

	o := configfile.Options{
		GeneratedBy:      "agent config init",
		Scenario:         opts.scenario,
		Workspace:        ".",
		Thinking:         "auto",
		SecurityMode:     "default",
		EnableMemory:     true,
		CredentialMethod: "file",
		Profiles:         map[string]configfile.ProfileConfig{},
		MCPServers:       map[string]configfile.MCPServerSetup{},
	}
	o.ApplyScenario()
	if opts.provider != "" {
		o.Provider = opts.provider
	}
	o.SetDefaultModel()
	if opts.model != "" {
		o.Model = opts.model
	}
	if o.Model == "" {
		return configfile.Options{}, fmt.Errorf("provider %q has no default model; pass --model", o.Provider)
	}
	o.BaseURL = configfile.DefaultBaseURL(o.Provider)
	if configfile.NeedsBaseURL(o.Provider) && o.BaseURL == "" {
		return configfile.Options{}, fmt.Errorf("provider %q needs a base_url; run `agent setup` or edit agent.toml after init", o.Provider)
	}

	o.SmallLLMProvider = o.Provider
	o.SetDefaultSmallModel()
	if opts.smallModel != "" {
		o.SmallLLMEnabled, o.SmallLLMModel = true, opts.smallModel
	}
	if o.SmallLLMEnabled && o.SmallLLMModel == "" {
		o.SmallLLMEnabled = false
	}
	if o.UseProfiles {
		o.ConfigureDefaultProfiles()
	}

	if opts.apiKeyEnv != "" {
		o.CredentialMethod, o.APIKeyEnv = "env", opts.apiKeyEnv
	}
	return o, nil
}

// writeAPIKey stores key for provider in the credentials file at path,
// preserving any providers already recorded there.
func writeAPIKey(path, provider, key string) (string, error) {
	store, err := credentials.NewFileStore(path)
	if err != nil {
		return "", fmt.Errorf("load credentials: %w", err)
	}
	if store == nil { // an empty file yields a nil store
		store = credentials.FileStore{}
	}
	store.SetAPIKey(provider, key)
	if err := store.Save(path); err != nil {
		return "", fmt.Errorf("save credentials: %w", err)
	}
	return path, nil
}

// ---------------------------------------------------------------------------
// show

func newConfigShowCmd(d deps) *cobra.Command {
	var (
		tf       targetFlags
		resolved bool
	)
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the configuration files in effect",
		Long: "Print each configuration file that is in effect, headed by the path it\n" +
			"came from. API keys and tokens are redacted. With --resolved, print the\n" +
			"merged agent.toml as TOML instead of the files themselves.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tg, err := tf.resolve(d)
			if err != nil {
				return err
			}
			if resolved {
				return showResolved(cmd.OutOrStdout(), tg)
			}
			return showFiles(cmd.OutOrStdout(), tg)
		},
	}
	tf.bind(cmd)
	cmd.Flags().BoolVar(&resolved, "resolved", false, "Print the merged configuration as TOML")
	return cmd
}

func showFiles(out io.Writer, t target) error {
	for i, f := range t.filesets() {
		if i > 0 {
			fmt.Fprintln(out)
		}
		active := f.active()
		if len(active) == 0 {
			fmt.Fprintf(out, "%s: (none of %s)\n", f.name, strings.Join(displayAll(f.candidates, t.home), ", "))
			continue
		}
		// Only a merged fileset can contribute more than one source.
		header := strings.Join(displayAll(active, t.home), " + ")
		if len(active) > 1 {
			header += " (merged)"
		}
		fmt.Fprintf(out, "%s: %s\n", f.name, header)
		for _, p := range active {
			body, err := os.ReadFile(p)
			if err != nil {
				return fmt.Errorf("read %s: %w", p, err)
			}
			safe, err := redactTOML(string(body))
			if err != nil {
				return fmt.Errorf("%s: %w", display(p, t.home), err)
			}
			fmt.Fprintln(out, safe)
		}
	}
	return nil
}

func showResolved(out io.Writer, t target) error {
	cfg, err := t.loadConfig()
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encode resolved config: %w", err)
	}
	safe, err := redactTOML(buf.String())
	if err != nil {
		return fmt.Errorf("encode resolved config: %w", err)
	}
	fmt.Fprintln(out, safe)
	return nil
}

func displayAll(paths []string, home string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = display(p, home)
	}
	return out
}

// secretKey matches a TOML key whose value is credential material. It is
// deliberately loose: over-redacting a harmless value costs nothing, while
// missing one puts a live key in a pasted bug report.
var secretKey = regexp.MustCompile(`(?i)api_?key|token|secret|password|authorization`)

// opaqueTable reports whether every value in the table at path is secret
// whatever its keys are called. Both tables hold user-supplied name/value
// pairs — outbound telemetry headers and an MCP server's environment — so no
// key-name rule can cover them.
func opaqueTable(path []string) bool {
	switch {
	case len(path) == 2 && path[0] == "telemetry" && path[1] == "headers":
		return true
	case len(path) == 4 && path[0] == "mcp" && path[1] == "servers" && path[3] == "env":
		return true
	}
	return false
}

// redactTOML masks every secret in TOML text and re-encodes it. Redaction
// works on the decoded document rather than the raw text so it sees values
// however they were quoted and however deeply they are nested; the cost is
// that comments do not survive, and that text which does not parse is
// reported as an error rather than printed unredacted.
func redactTOML(text string) (string, error) {
	var doc map[string]any
	if _, err := toml.Decode(text, &doc); err != nil {
		return "", err
	}
	redactTree(doc, nil)
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// redactTree walks a decoded TOML document, masking string values whose key
// names a secret or whose table is opaque.
func redactTree(node map[string]any, path []string) {
	opaque := opaqueTable(path)
	for k, v := range node {
		child := append(slices.Clone(path), k)
		switch value := v.(type) {
		case map[string]any:
			redactTree(value, child)
		case []any:
			for _, elem := range value {
				if table, ok := elem.(map[string]any); ok {
					redactTree(table, child)
				}
			}
		case string:
			if opaque || secretKey.MatchString(k) {
				node[k] = redact(value)
			}
		}
	}
}

// redact masks a secret, keeping just enough of a long one to tell two keys
// apart. A short value is hidden completely — there is too little of it for a
// stub to be anything but a hint at the whole.
func redact(v string) string {
	if len(v) < 8 {
		return "***"
	}
	return v[:3] + "…" + v[len(v)-4:]
}

// ---------------------------------------------------------------------------
// validate

func newConfigValidateCmd(d deps) *cobra.Command {
	var tf targetFlags
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check the configuration files for problems",
		Long: "Parse every configuration file in effect with the same loaders `agent run`\n" +
			"uses and report each problem found. Exits non-zero when a file fails to\n" +
			"load. A configured provider with no credential anywhere is reported as a\n" +
			"warning, since the credential may legitimately arrive from the environment\n" +
			"at run time.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tg, err := tf.resolve(d)
			if err != nil {
				return err
			}
			return runConfigValidate(cmd.OutOrStdout(), d, tg)
		},
	}
	tf.bind(cmd)
	return cmd
}

func runConfigValidate(out io.Writer, d deps, t target) error {
	files := t.filesets()
	cfg, problems := validateAgentFile(t, files[0])
	problems = append(problems, validatePolicyFile(t, files[1], cfg)...)
	problems = append(problems, validateCredentialsFile(t, files[2])...)
	warnings := credentialWarnings(d, t, files[2], cfg)

	for _, w := range warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	if len(problems) == 0 {
		fmt.Fprintln(out, "✓ Configuration is valid")
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d configuration problem(s):", len(problems))
	for _, p := range problems {
		fmt.Fprintf(&b, "\n  %s", p)
	}
	return errors.New(b.String())
}

// validateAgentFile loads every agent.toml source individually so a broken
// file is named, then returns the effective config for the later checks.
func validateAgentFile(t target, f fileset) (*config.Config, []string) {
	var problems []string
	for _, p := range f.active() {
		if _, err := config.LoadFile(p); err != nil {
			problems = append(problems, prefixPath(display(p, t.home), err))
		}
	}
	cfg, err := t.loadConfig()
	if err != nil {
		if len(problems) == 0 {
			problems = append(problems, err.Error())
		}
		return config.New(), problems
	}
	for _, dep := range cfg.Deprecations {
		problems = append(problems, fmt.Sprintf("%s: %s", f.name, dep))
	}
	return cfg, problems
}

// validatePolicyFile parses the effective policy through the same loader and
// legacy-key report `agent run` uses.
func validatePolicyFile(t target, f fileset, cfg *config.Config) []string {
	active := f.active()
	if len(active) == 0 {
		return nil // no policy file is legal: every tool is enabled
	}
	path := active[0]
	body, err := os.ReadFile(path)
	if err != nil {
		return []string{prefixPath(display(path, t.home), err)}
	}
	_, unknown, err := policy.FromTOMLWithUnknownKeys(string(body), cfg.Agent.Workspace, t.home)
	if err != nil {
		return []string{prefixPath(display(path, t.home), err)}
	}
	if err := run.ValidatePolicyKeys(display(path, t.home), unknown); err != nil {
		return []string{err.Error()}
	}
	return nil
}

// validateCredentialsFile surfaces an unreadable, insecure or malformed
// credentials file.
func validateCredentialsFile(t target, f fileset) []string {
	active := f.active()
	if len(active) == 0 {
		return nil
	}
	if _, err := credentials.NewFileStore(active[0]); err != nil {
		return []string{prefixPath(display(active[0], t.home), err)}
	}
	return nil
}

// prefixPath names the file a problem came from, unless the loader already
// named it — a problem is never reported as "agent.toml: agent.toml: ...".
func prefixPath(path string, err error) string {
	if strings.Contains(err.Error(), path) {
		return err.Error()
	}
	return path + ": " + err.Error()
}

// credentialWarnings reports configured providers with no credential in the
// file, the environment or the Claude CLI. Local providers serve models
// without authenticating, so they are exempt.
func credentialWarnings(d deps, t target, credsFile fileset, cfg *config.Config) []string {
	providers := []string{cfg.LLM.Provider, cfg.SmallLLM.Provider}
	slices.Sort(providers)
	providers = slices.Compact(providers)

	var wanted []string
	for _, p := range providers {
		if p != "" && !localProvider(p) {
			wanted = append(wanted, p)
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	var override string
	if active := credsFile.active(); len(active) > 0 && t.explicit {
		override = active[0]
	}
	creds, err := d.credentials(override)
	if err != nil {
		return []string{fmt.Sprintf("could not read credentials: %v", err)}
	}

	var warnings []string
	for _, p := range wanted {
		if creds.Get(p) == "" && !envKeySet(d, cfg, p) {
			warnings = append(warnings, fmt.Sprintf("provider %q is configured in agent.toml but no credential was found", p))
		}
	}
	return warnings
}

// envKeySet reports whether the env var agent.toml names for the provider is
// populated. credentials.EnvStore only knows the conventional variables, so
// a custom api_key_env has to be checked here.
func envKeySet(d deps, cfg *config.Config, provider string) bool {
	for _, llm := range []config.LLMConfig{cfg.LLM, cfg.SmallLLM} {
		if llm.Provider == provider && llm.APIKeyEnv != "" && d.getenv(llm.APIKeyEnv) != "" {
			return true
		}
	}
	return false
}

// localProvider reports whether a provider runs on the user's own machine and
// therefore needs no credential.
func localProvider(p string) bool {
	return p == configfile.ProviderOllamaLocal || p == configfile.ProviderLMStudio
}

// ---------------------------------------------------------------------------
// path

func newConfigPathCmd(d deps) *cobra.Command {
	var tf targetFlags
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Show where each configuration file is looked for",
		Long: "List, per configuration file, every path that is consulted in precedence\n" +
			"order and whether it exists.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tg, err := tf.resolve(d)
			if err != nil {
				return err
			}
			runConfigPath(cmd.OutOrStdout(), tg)
			return nil
		},
	}
	tf.bind(cmd)
	return cmd
}

func runConfigPath(out io.Writer, t target) {
	for _, f := range t.filesets() {
		fmt.Fprintln(out, f.name)
		active := f.active()
		for _, p := range f.candidates {
			state := "missing"
			switch {
			case slices.Contains(active, p):
				state = "in effect"
			case fileExists(p):
				state = "shadowed" // exists, but a higher-precedence file won
			}
			fmt.Fprintf(out, "  %-48s %s\n", display(p, t.home), state)
		}
	}
}
