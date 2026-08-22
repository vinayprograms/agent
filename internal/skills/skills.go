// Package skills discovers and loads agent skills from the filesystem.
// Each skill is a folder containing a SKILL.md with instructions that
// get injected into agent context on activation.
package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a loaded Agent Skill.
type Skill struct {
	// From frontmatter
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	AllowedTools  string            `yaml:"allowed-tools,omitempty"`

	// From content
	Instructions string `yaml:"-"`

	// Location
	Path string `yaml:"-"`
}

// Load loads a skill from a directory.
func Load(skillDir string) (*Skill, error) {
	skillPath := filepath.Join(skillDir, "SKILL.md")

	content, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("reading skill: %w", err)
	}

	skill, err := Parse(string(content))
	if err != nil {
		return nil, err
	}

	skill.Path = skillDir

	// Validate name matches directory
	dirName := filepath.Base(skillDir)
	if skill.Name != dirName {
		return nil, fmt.Errorf("skill name %q does not match directory name %q", skill.Name, dirName)
	}

	return skill, nil
}

// Parse parses a SKILL.md file content.
func Parse(content string) (*Skill, error) {
	// Split frontmatter and body
	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, err
	}

	skill := &Skill{}
	if err := yaml.Unmarshal([]byte(frontmatter), skill); err != nil {
		return nil, fmt.Errorf("invalid frontmatter: %w", err)
	}

	// Validate required fields
	if skill.Name == "" {
		return nil, fmt.Errorf("missing required field: name")
	}
	if skill.Description == "" {
		return nil, fmt.Errorf("missing required field: description")
	}

	// Validate name format
	if err := validateName(skill.Name); err != nil {
		return nil, err
	}

	skill.Instructions = strings.TrimSpace(body)
	return skill, nil
}

// splitFrontmatter extracts YAML frontmatter from markdown.
func splitFrontmatter(content string) (frontmatter, body string, err error) {
	lines := strings.Split(content, "\n")

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", fmt.Errorf("missing frontmatter delimiter")
	}

	var fmLines []string
	var bodyStart int
	inFrontmatter := true

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			inFrontmatter = false
			bodyStart = i + 1
			break
		}
		if inFrontmatter {
			fmLines = append(fmLines, lines[i])
		}
	}

	if inFrontmatter {
		return "", "", fmt.Errorf("unclosed frontmatter")
	}

	frontmatter = strings.Join(fmLines, "\n")
	if bodyStart < len(lines) {
		body = strings.Join(lines[bodyStart:], "\n")
	}

	return frontmatter, body, nil
}

// validateName validates a skill name per spec.
func validateName(name string) error {
	if len(name) == 0 || len(name) > 64 {
		return fmt.Errorf("name must be 1-64 characters")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("name cannot start or end with hyphen")
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("name cannot contain consecutive hyphens")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return fmt.Errorf("name can only contain lowercase letters, numbers, and hyphens")
		}
	}
	return nil
}

// ReadReference reads a reference file from the skill's references directory.
// name is untrusted (it comes from LLM output) and must stay inside that
// directory: absolute names and ".." segments are rejected.
func (s *Skill) ReadReference(name string) (string, error) {
	refPath, err := subpath(filepath.Join(s.Path, "references"), name)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(refPath)
	if err != nil {
		return "", fmt.Errorf("reading reference %q: %w", name, err)
	}
	return string(content), nil
}

// ListScripts lists available scripts in the skill.
func (s *Skill) ListScripts() ([]string, error) {
	scriptsDir := filepath.Join(s.Path, "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var scripts []string
	for _, entry := range entries {
		if !entry.IsDir() {
			scripts = append(scripts, entry.Name())
		}
	}
	return scripts, nil
}

// ScriptPath returns the full path to a script. name is subject to the same
// containment rule as ReadReference.
func (s *Skill) ScriptPath(name string) (string, error) {
	return subpath(filepath.Join(s.Path, "scripts"), name)
}

// subpath joins an untrusted name under a trusted dir, refusing names that
// would resolve outside it.
func subpath(dir, name string) (string, error) {
	clean := filepath.Clean(name)
	if name == "" || filepath.IsAbs(clean) || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid skill file name %q: must be relative to the skill directory", name)
	}
	return filepath.Join(dir, clean), nil
}
