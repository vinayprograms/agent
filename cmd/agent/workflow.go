// Workflow loading: gathers everything the agent needs before execution —
// config, Agentfile, and policy.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/policy"
)

// workflow handles the configuration phase of a run.
type workflow struct {
	// Parsed from CLI (populated via RunCmd)
	agentfilePath string
	inputs        map[string]string
	configPath    string
	policyPath    string
	workspacePath string
	statePath     string // CLI --state override
	debug         bool
	sessionLabel  string // Override session directory name (default: Agentfile NAME)

	// Loaded artifacts
	wf      *agentfile.Workflow
	cfg     *config.Config
	pol     *policy.Policy
	baseDir string
}

// load loads config, agentfile, and policy.
func (w *workflow) load() error {
	if err := w.loadConfig(); err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := w.loadAgentfile(); err != nil {
		return fmt.Errorf("loading Agentfile: %w", err)
	}
	if err := w.loadPolicy(); err != nil {
		return fmt.Errorf("loading policy: %w", err)
	}
	return nil
}

// loadConfig loads and applies configuration.
func (w *workflow) loadConfig() error {
	var err error
	w.cfg, err = config.LoadWithPrecedence(config.LoadOptions{
		CLIPath: w.configPath,
	})
	if err != nil {
		return err
	}

	// Strict validation: workspace conflict
	if err := w.validateWorkspaceConfig(); err != nil {
		return err
	}

	// Strict validation: state location conflict
	if err := w.validateStateConfig(); err != nil {
		return err
	}

	// Apply CLI overrides (after validation)
	if w.workspacePath != "" {
		w.cfg.Agent.Workspace = w.workspacePath
	}
	if w.cfg.Agent.Workspace == "" {
		w.cfg.Agent.Workspace, _ = os.Getwd()
	}
	w.cfg.Agent.Workspace = expandAbsPath(w.cfg.Agent.Workspace)
	return nil
}

// expandAbsPath expands ~ and resolves to absolute path.
func expandAbsPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p, _ = filepath.Abs(p)
	}
	return p
}

// validateWorkspaceConfig checks for conflicting workspace settings.
func (w *workflow) validateWorkspaceConfig() error {
	if w.workspacePath == "" || w.cfg.Agent.Workspace == "" {
		return nil
	}
	cliResolved := expandAbsPath(w.workspacePath)
	cfgResolved := expandAbsPath(w.cfg.Agent.Workspace)
	if cliResolved != cfgResolved {
		return fmt.Errorf("workspace conflict: --workspace=%q resolves to %q, agent.toml has %q",
			w.workspacePath, cliResolved, cfgResolved)
	}
	return nil
}

// validateStateConfig checks for conflicting state location settings.
// CLI state path may be a subdirectory of agent.toml's value (swarm isolates
// per-agent state under a shared root), so we allow subdirectories.
// When --state is provided, it always wins — swarm uses this to isolate
// per-agent state and prevent Bleve index lock conflicts.
func (w *workflow) validateStateConfig() error {
	// --state always overrides config — no conflict check needed.
	// The caller explicitly chose the state path.
	return nil
}

// loadAgentfile parses and validates the Agentfile.
func (w *workflow) loadAgentfile() error {
	var err error
	w.wf, err = agentfile.LoadFile(w.agentfilePath)
	if err != nil {
		return err
	}
	w.baseDir = filepath.Dir(w.agentfilePath)
	return nil
}

// loadPolicy loads the policy file. Legacy (pre-v1.2.0 schema) keys are
// rejected loudly with the replacement named for each one (A-C1).
func (w *workflow) loadPolicy() error {
	policyPath := w.policyPath
	if policyPath == "" {
		policyPath = filepath.Join(w.baseDir, "policy.toml")
	}
	home, _ := os.UserHomeDir()

	content, err := os.ReadFile(policyPath)
	switch {
	case os.IsNotExist(err) && w.policyPath == "":
		// No policy file: keep the pre-migration behaviour (every tool
		// enabled) so `agent run` works out of the box, but say so.
		fmt.Fprintf(os.Stderr, "warning: no policy file at %s; all tools enabled\n", policyPath)
		w.pol = policy.New()
		w.pol.DefaultDeny = false
	case err != nil:
		return fmt.Errorf("failed to read policy file: %w", err)
	default:
		pol, unknown, err := policy.FromTOMLWithUnknownKeys(string(content), w.cfg.Agent.Workspace, home)
		if err != nil {
			return err
		}
		if err := validatePolicyKeys(policyPath, unknown); err != nil {
			return fmt.Errorf("policy validation: %w", err)
		}
		w.pol = pol
	}

	// Ensure workspace is always in allowed_dirs (universal filesystem boundary).
	// If no allowed_dirs are configured, default to workspace.
	// If allowed_dirs exist but don't include workspace, add it.
	w.ensureWorkspaceInAllowedDirs()
	return nil
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
func validatePolicyKeys(path string, unknown []string) error {
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
func (w *workflow) ensureWorkspaceInAllowedDirs() {
	ws := w.cfg.Agent.Workspace
	if ws == "" {
		return
	}

	if len(w.pol.AllowedDirs) == 0 {
		w.pol.AllowedDirs = []string{ws}
		return
	}

	// Check if workspace is already covered (exact match or subdirectory of an allowed dir)
	for _, d := range w.pol.AllowedDirs {
		resolved := d
		// Expand $WORKSPACE in existing entries
		if resolved == "$WORKSPACE" {
			return // already references workspace
		}
		if resolved == ws || strings.HasPrefix(ws, resolved+string(filepath.Separator)) {
			return // workspace already covered
		}
	}

	w.pol.AllowedDirs = append(w.pol.AllowedDirs, ws)
}
