// Package policy provides security policy loading and enforcement.
package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ProtectedFiles are files that agents cannot modify (security-critical).
var ProtectedFiles = []string{
	"agent.toml",
	"credentials.toml",
	"policy.toml",
}

// Policy represents the security policy for the agent.
type Policy struct {
	DefaultDeny bool
	Workspace   string
	HomeDir     string
	ConfigDir   string // Directory containing agent.toml, policy.toml
	Tools       map[string]*ToolPolicy
}

// ToolPolicy represents the policy for a specific tool.
type ToolPolicy struct {
	Enabled      bool
	Allow        []string
	Deny         []string
	Allowlist    []string // for bash
	Denylist     []string // for bash
	AllowDomains []string // for web tools
	RateLimit    int      // requests per minute
}

// tomlPolicy is the TOML representation.
type tomlPolicy struct {
	DefaultDeny bool                   `toml:"default_deny"`
	Tools       map[string]tomlTool    `toml:"-"`
}

type tomlTool struct {
	Enabled      bool     `toml:"enabled"`
	Allow        []string `toml:"allow"`
	Deny         []string `toml:"deny"`
	Allowlist    []string `toml:"allowlist"`
	Denylist     []string `toml:"denylist"`
	AllowDomains []string `toml:"allow_domains"`
	RateLimit    int      `toml:"rate_limit"`
}

// New creates a new policy with defaults.
func New() *Policy {
	homeDir, _ := os.UserHomeDir()
	return &Policy{
		DefaultDeny: false,
		HomeDir:     homeDir,
		Tools:       make(map[string]*ToolPolicy),
	}
}

// LoadFile loads a policy from a TOML file.
func LoadFile(path string) (*Policy, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file: %w", err)
	}
	return Parse(string(content))
}

// Parse parses a policy from TOML content.
func Parse(content string) (*Policy, error) {
	// First pass: get default_deny
	var base struct {
		DefaultDeny bool `toml:"default_deny"`
	}
	if _, err := toml.Decode(content, &base); err != nil {
		return nil, fmt.Errorf("failed to parse policy: %w", err)
	}

	pol := New()
	pol.DefaultDeny = base.DefaultDeny

	// Second pass: parse tool sections
	var raw map[string]interface{}
	if _, err := toml.Decode(content, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse policy: %w", err)
	}

	for key, value := range raw {
		if key == "default_deny" {
			continue
		}

		// Try to parse as tool config
		toolMap, ok := value.(map[string]interface{})
		if !ok {
			continue
		}

		tp := &ToolPolicy{Enabled: true}

		if v, ok := toolMap["enabled"].(bool); ok {
			tp.Enabled = v
		}
		if v, ok := toolMap["allow"].([]interface{}); ok {
			tp.Allow = toStringSlice(v)
		}
		if v, ok := toolMap["deny"].([]interface{}); ok {
			tp.Deny = toStringSlice(v)
		}
		if v, ok := toolMap["allowlist"].([]interface{}); ok {
			tp.Allowlist = toStringSlice(v)
		}
		if v, ok := toolMap["denylist"].([]interface{}); ok {
			tp.Denylist = toStringSlice(v)
		}
		if v, ok := toolMap["allow_domains"].([]interface{}); ok {
			tp.AllowDomains = toStringSlice(v)
		}
		if v, ok := toolMap["rate_limit"].(int64); ok {
			tp.RateLimit = int(v)
		}

		pol.Tools[key] = tp
	}

	return pol, nil
}

func toStringSlice(v []interface{}) []string {
	result := make([]string, 0, len(v))
	for _, item := range v {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// GetToolPolicy returns the policy for a tool, with defaults.
func (p *Policy) GetToolPolicy(tool string) *ToolPolicy {
	if tp, ok := p.Tools[tool]; ok {
		return tp
	}
	// Return default policy
	return &ToolPolicy{Enabled: true}
}

// IsToolEnabled checks if a tool is enabled.
func (p *Policy) IsToolEnabled(tool string) bool {
	tp := p.GetToolPolicy(tool)
	return tp.Enabled
}

// IsProtectedFile checks if a path refers to a protected config file.
// Protected files cannot be modified by the agent.
// Resolves symlinks to prevent bypass attacks.
func (p *Policy) IsProtectedFile(path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	
	// Resolve symlinks to get the real path
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// If file doesn't exist yet, check the path as-is
		// but also check if parent exists and resolve that
		dir := filepath.Dir(absPath)
		if realDir, err := filepath.EvalSymlinks(dir); err == nil {
			realPath = filepath.Join(realDir, filepath.Base(absPath))
		} else {
			realPath = absPath
		}
	}
	
	baseName := filepath.Base(realPath)
	
	// Check against protected file names
	for _, protected := range ProtectedFiles {
		if baseName == protected {
			return true
		}
	}
	
	// Also protect files in config directory
	if p.ConfigDir != "" {
		configAbs, _ := filepath.Abs(p.ConfigDir)
		configReal, _ := filepath.EvalSymlinks(configAbs)
		if configReal == "" {
			configReal = configAbs
		}
		for _, protected := range ProtectedFiles {
			protectedPath := filepath.Join(configReal, protected)
			if realPath == protectedPath {
				return true
			}
		}
	}
	
	// Protect ~/.config/grid/credentials.toml
	if p.HomeDir != "" {
		credPath := filepath.Join(p.HomeDir, ".config", "grid", "credentials.toml")
		credReal, _ := filepath.EvalSymlinks(credPath)
		if credReal == "" {
			credReal = credPath
		}
		if realPath == credReal {
			return true
		}
	}
	
	return false
}

// CheckPath checks if a path is allowed for a tool.
func (p *Policy) CheckPath(tool, path string) (bool, string) {
	tp := p.GetToolPolicy(tool)
	if !tp.Enabled {
		return false, fmt.Sprintf("tool %s is disabled", tool)
	}

	// Expand path to absolute
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	// Check protected files first (write/edit tools)
	if tool == "write" || tool == "edit" {
		if p.IsProtectedFile(absPath) {
			return false, fmt.Sprintf("path %s is a protected config file", path)
		}
	}

	// Check deny patterns first (deny wins)
	for _, pattern := range tp.Deny {
		expanded := p.expandPattern(pattern)
		if matchPath(expanded, absPath) {
			return false, fmt.Sprintf("path %s matches deny pattern %s", path, pattern)
		}
	}

	// Check allow patterns
	if len(tp.Allow) > 0 {
		for _, pattern := range tp.Allow {
			expanded := p.expandPattern(pattern)
			if matchPath(expanded, absPath) {
				return true, ""
			}
		}
		// Allow patterns defined but none matched - deny
		return false, fmt.Sprintf("path %s not in allow list", path)
	}

	// No allow patterns defined - check default_deny
	if p.DefaultDeny {
		return false, fmt.Sprintf("path %s not in allow list (default_deny=true)", path)
	}

	return true, ""
}

// CheckCommand checks if a command is allowed for bash.
func (p *Policy) CheckCommand(tool, cmd string) (bool, string) {
	tp := p.GetToolPolicy(tool)
	if !tp.Enabled {
		return false, fmt.Sprintf("tool %s is disabled", tool)
	}

	// Check denylist first (deny wins)
	for _, pattern := range tp.Denylist {
		if matchCommand(pattern, cmd) {
			return false, fmt.Sprintf("command matches deny pattern: %s", pattern)
		}
	}

	// Check allowlist (if defined, command must match)
	if len(tp.Allowlist) > 0 {
		for _, pattern := range tp.Allowlist {
			if matchCommand(pattern, cmd) {
				return true, ""
			}
		}
		return false, "command not in allowlist"
	}

	return true, ""
}

// CheckDomain checks if a domain is allowed for web tools.
func (p *Policy) CheckDomain(tool, domain string) (bool, string) {
	tp := p.GetToolPolicy(tool)
	if !tp.Enabled {
		return false, fmt.Sprintf("tool %s is disabled", tool)
	}

	// If no domains specified, allow all
	if len(tp.AllowDomains) == 0 {
		return true, ""
	}

	for _, pattern := range tp.AllowDomains {
		if matchDomain(pattern, domain) {
			return true, ""
		}
	}

	return false, fmt.Sprintf("domain %s not in allow list", domain)
}

// expandPattern expands $WORKSPACE and ~ in patterns.
func (p *Policy) expandPattern(pattern string) string {
	if strings.HasPrefix(pattern, "$WORKSPACE") {
		pattern = strings.Replace(pattern, "$WORKSPACE", p.Workspace, 1)
	}
	if strings.HasPrefix(pattern, "~") {
		pattern = strings.Replace(pattern, "~", p.HomeDir, 1)
	}
	return pattern
}

// matchPath matches a path against a glob pattern.
func matchPath(pattern, path string) bool {
	// Normalize paths
	pattern = filepath.Clean(pattern)
	path = filepath.Clean(path)
	
	// Handle ** patterns (recursive match)
	if strings.Contains(pattern, "**") {
		parts := strings.SplitN(pattern, "**", 2)
		prefix := strings.TrimSuffix(parts[0], string(filepath.Separator))
		suffix := ""
		if len(parts) > 1 {
			suffix = strings.TrimPrefix(parts[1], string(filepath.Separator))
		}
		
		// Path must start with prefix
		if prefix != "" && !strings.HasPrefix(path, prefix) {
			return false
		}
		
		// Get the part of path after prefix
		remaining := path
		if prefix != "" {
			remaining = strings.TrimPrefix(path, prefix)
			remaining = strings.TrimPrefix(remaining, string(filepath.Separator))
		}
		
		// If no suffix, match any remaining path
		if suffix == "" {
			return true
		}
		
		// Suffix must match at the end
		if strings.HasSuffix(remaining, suffix) {
			return true
		}
		
		// Or match using glob
		matched, _ := filepath.Match(suffix, filepath.Base(remaining))
		return matched
	}

	// For * patterns (single segment), use filepath.Match
	// But we need to match against the full path
	matched, _ := filepath.Match(pattern, path)
	return matched
}

// matchGlob matches using filepath.Match semantics.
func matchGlob(pattern, path string) bool {
	matched, _ := filepath.Match(pattern, path)
	if matched {
		return true
	}
	
	// Also try matching just the base name
	matched, _ = filepath.Match(pattern, filepath.Base(path))
	return matched
}

// matchCommand matches a command against a pattern.
func matchCommand(pattern, cmd string) bool {
	// Split pattern and command into words
	patternWords := strings.Fields(pattern)
	cmdWords := strings.Fields(cmd)

	if len(patternWords) == 0 {
		return false
	}

	// First word must match (command name)
	if patternWords[0] != cmdWords[0] {
		return false
	}

	// If pattern ends with *, match any additional args
	if len(patternWords) >= 2 && patternWords[len(patternWords)-1] == "*" {
		return true
	}

	// Exact match check
	return len(patternWords) == len(cmdWords)
}

// matchDomain matches a domain against a pattern.
func matchDomain(pattern, domain string) bool {
	if pattern == "*" {
		return true
	}

	// Wildcard subdomain matching
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(domain, suffix) || domain == strings.TrimPrefix(suffix, ".")
	}

	return pattern == domain
}

// NewRestrictive creates a restrictive policy that denies everything by default.
func NewRestrictive() *Policy {
	return &Policy{
		DefaultDeny: true,
		Tools:       make(map[string]*ToolPolicy),
	}
}
