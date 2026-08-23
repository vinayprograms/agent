// Package run loads an agent's configuration and executes its workflow:
// Load gathers config, Agentfile and policy; New wires the runtime
// components; Runtime.Run dispatches the workflow.
package run

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/policy"
)

// LoadOptions selects what to load and from where. The zero value loads
// "Agentfile" from the working directory with the standard config
// precedence.
type LoadOptions struct {
	AgentfilePath string            // default "Agentfile"; ignored when Goal is set
	ConfigPath    string            // explicit agent.toml; empty uses the standard precedence
	PolicyPath    string            // explicit policy.toml; empty looks next to the Agentfile
	Workspace     string            // --workspace override
	Goal          string            // inline goal; when set no Agentfile is read
	Inputs        map[string]string // workflow inputs
	Debug         bool
	SessionLabel  string    // deployment label recorded in the session header
	Home          string    // home directory for ~ expansion; empty asks the OS
	Stderr        io.Writer // warnings (deprecations, missing policy); nil discards
}

// Loaded is everything an agent needs before execution. Callers may adjust
// the exported fields (serve mode overlays [service] settings) before
// passing it to New.
type Loaded struct {
	Workflow     *agentfile.Workflow
	Config       *config.Config
	Policy       *policy.Policy
	Inputs       map[string]string
	Debug        bool
	Agentfile    string // absolute Agentfile path; empty for an inline goal
	SessionLabel string

	home string
}

// inlineGoalName is the workflow name given to a --goal run. Runtime.Run
// prints raw outputs rather than a JSON result for it.
const inlineGoalName = "inline-goal"

// inlineGoal builds the single-goal workflow that backs `agent run --goal`.
func inlineGoal(text string) *agentfile.Workflow {
	return &agentfile.Workflow{
		Name:  inlineGoalName,
		Goals: []agentfile.Goal{{Name: "goal", Outcome: text}},
		Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "run-goal", UsingGoals: []string{"goal"}}},
	}
}

// Load reads config, Agentfile and policy, applies the CLI overrides and
// validates the result.
func Load(opts LoadOptions) (*Loaded, error) {
	home := opts.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	warn := opts.Stderr
	if warn == nil {
		warn = io.Discard
	}
	l := &Loaded{
		Inputs:       opts.Inputs,
		Debug:        opts.Debug,
		SessionLabel: opts.SessionLabel,
		home:         home,
	}

	if err := l.loadConfig(opts, warn); err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	baseDir := "."
	if opts.Goal != "" {
		l.Workflow = inlineGoal(opts.Goal)
	} else {
		path := opts.AgentfilePath
		if path == "" {
			path = "Agentfile"
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil, fmt.Errorf("%s not found", path)
		}
		wf, err := agentfile.LoadFile(path)
		if err != nil {
			return nil, fmt.Errorf("loading Agentfile: %w", err)
		}
		l.Workflow = wf
		l.Agentfile, _ = filepath.Abs(path)
		baseDir = filepath.Dir(path)
	}

	if err := l.loadPolicy(opts.PolicyPath, baseDir, warn); err != nil {
		return nil, fmt.Errorf("loading policy: %w", err)
	}
	return l, nil
}

// loadConfig loads agent.toml with the standard precedence and applies the
// --workspace override, rejecting a workspace that conflicts with the file.
func (l *Loaded) loadConfig(opts LoadOptions, warn io.Writer) error {
	cfg, err := config.LoadWithPrecedence(config.LoadOptions{CLIPath: opts.ConfigPath})
	if err != nil {
		return err
	}
	l.Config = cfg
	for _, d := range cfg.Deprecations {
		fmt.Fprintf(warn, "WARN: %s\n", d)
	}
	for _, uk := range cfg.UnknownKeys {
		fmt.Fprintf(warn, "WARN: %s (ignored)\n", uk)
	}

	if opts.Workspace != "" && cfg.Agent.Workspace != "" {
		cliResolved := l.absPath(opts.Workspace)
		cfgResolved := l.absPath(cfg.Agent.Workspace)
		if cliResolved != cfgResolved {
			return fmt.Errorf("workspace conflict: --workspace=%q resolves to %q, agent.toml has %q",
				opts.Workspace, cliResolved, cfgResolved)
		}
	}
	// The CLI workspace wins once it has been checked against the file.
	if opts.Workspace != "" {
		cfg.Agent.Workspace = opts.Workspace
	}
	if cfg.Agent.Workspace == "" {
		cfg.Agent.Workspace, _ = os.Getwd()
	}
	cfg.Agent.Workspace = l.absPath(cfg.Agent.Workspace)
	return nil
}

// absPath expands ~ and resolves p against the working directory.
func (l *Loaded) absPath(p string) string {
	p = config.ExpandHome(p, l.home)
	if !filepath.IsAbs(p) {
		p, _ = filepath.Abs(p)
	}
	return p
}

// loadPolicy loads the policy file. Legacy (pre-v1.2.0 schema) keys are
// rejected loudly with the replacement named for each one (A-C1).
func (l *Loaded) loadPolicy(explicitPath, baseDir string, warn io.Writer) error {
	policyPath, content, err := l.resolvePolicyFile(explicitPath, baseDir, warn)
	if err != nil {
		return err
	}
	if content == nil {
		// No policy file anywhere: keep the pre-migration behaviour (every
		// tool enabled) so `agent run` works out of the box.
		l.Policy = policy.New()
		l.Policy.DefaultDeny = false
	} else {
		pol, unknown, err := policy.FromTOMLWithUnknownKeys(string(content), l.Config.Agent.Workspace, l.home)
		if err != nil {
			return err
		}
		if err := ValidatePolicyKeys(policyPath, unknown); err != nil {
			return fmt.Errorf("policy validation: %w", err)
		}
		l.Policy = pol
	}

	// Ensure workspace is always in allowed_dirs (universal filesystem boundary).
	// If no allowed_dirs are configured, default to workspace.
	// If allowed_dirs exist but don't include workspace, add it.
	l.ensureWorkspaceInAllowedDirs()
	return nil
}

// resolvePolicyFile finds the policy file per precedence: explicitPath (must
// exist and parse), else <baseDir>/policy.toml, else the user-level default
// at ~/.config/agent/policy.toml, else no file at all (permissive, with a
// warning naming both locations checked). A nil content with a nil error
// means "use the permissive default".
func (l *Loaded) resolvePolicyFile(explicitPath, baseDir string, warn io.Writer) (path string, content []byte, err error) {
	if explicitPath != "" {
		content, err = os.ReadFile(explicitPath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to read policy file: %w", err)
		}
		return explicitPath, content, nil
	}

	projectPath := filepath.Join(baseDir, "policy.toml")
	content, err = os.ReadFile(projectPath)
	switch {
	case err == nil:
		return projectPath, content, nil
	case !os.IsNotExist(err):
		return "", nil, fmt.Errorf("failed to read policy file: %w", err)
	}

	defaultPath := filepath.Join(config.DefaultConfigDir(l.home), "policy.toml")
	content, err = os.ReadFile(defaultPath)
	switch {
	case err == nil:
		return defaultPath, content, nil
	case !os.IsNotExist(err):
		return "", nil, fmt.Errorf("failed to read policy file: %w", err)
	}

	fmt.Fprintf(warn, "warning: no policy file at %s or %s; all tools enabled\n", projectPath, defaultPath)
	return "", nil, nil
}

// legacyPolicyKeys maps pre-v1.2.0 policy keys (by their last one or two
// dotted segments) to the replacement in the current schema.
var legacyPolicyKeys = map[string]string{
	"enabled":                 "list the tool under [tools.<name>]",
	"allowlist":               "(removed; use deny + LLM review)",
	"denylist":                "deny",
	"allow_domains":           "allow",
	"rate_limit":              "(removed)",
	"memory_read.enabled":     "list recall under [tools.recall]",
	"memory_write.enabled":    "list remember under [tools.remember]",
	"mcp.default_deny":        "mcp.enabled",
	"mcp.allowed_tools":       "mcp.allow",
	"security.extra_patterns": "content.security.patterns",
	"security.extra_keywords": "content.security.keywords",
}

// validatePolicyKeys turns the unknown keys reported by the policy parser
// into one error naming every key and its replacement. sandbox/timeout are
// valid schema keys and never appear here.
func ValidatePolicyKeys(path string, unknown []string) error {
	if len(unknown) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s uses keys the current policy schema does not recognise:", path)
	for _, key := range unknown {
		if isTableOf(key, unknown) {
			continue // the table is reported through its child keys
		}
		fmt.Fprintf(&b, "\n  %s -> %s", key, policyKeyReplacement(key))
	}
	return errors.New(b.String())
}

// isTableOf reports whether key is a bare table name that has child keys in
// the same unknown-key list.
func isTableOf(key string, keys []string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k, key+".") {
			return true
		}
	}
	return false
}

// policyKeyReplacement names the replacement for a legacy key, or "unknown
// key" when the key is not a known legacy spelling.
func policyKeyReplacement(key string) string {
	if r, ok := legacyPolicyKeys[key]; ok {
		return r
	}
	parts := strings.Split(key, ".")
	if len(parts) >= 2 {
		if r, ok := legacyPolicyKeys[strings.Join(parts[len(parts)-2:], ".")]; ok {
			return r
		}
	}
	if r, ok := legacyPolicyKeys[parts[len(parts)-1]]; ok {
		return r
	}
	return "unknown key (remove it)"
}

// ensureWorkspaceInAllowedDirs guarantees the project workspace is always
// in the policy's universal allowed_dirs list. The workspace comes from
// agent.toml (cfg.Agent.Workspace), not from policy.toml.
func (l *Loaded) ensureWorkspaceInAllowedDirs() {
	ws := l.Config.Agent.Workspace
	if ws == "" {
		return
	}

	if len(l.Policy.AllowedDirs) == 0 {
		l.Policy.AllowedDirs = []string{ws}
		return
	}

	// Check if workspace is already covered (exact match or subdirectory of an allowed dir)
	for _, d := range l.Policy.AllowedDirs {
		resolved := d
		// Expand $WORKSPACE in existing entries
		if resolved == "$WORKSPACE" {
			return // already references workspace
		}
		if resolved == ws || strings.HasPrefix(ws, resolved+string(filepath.Separator)) {
			return // workspace already covered
		}
	}

	l.Policy.AllowedDirs = append(l.Policy.AllowedDirs, ws)
}
