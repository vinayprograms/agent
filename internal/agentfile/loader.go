package agentfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinayprograms/agent/internal/skills"
)

// ValidationError is a single Agentfile validation failure. Line is 0 when
// the error is not associated with a specific line.
type ValidationError struct {
	Line int
	Msg  string
}

func (e *ValidationError) Error() string {
	if e.Line == 0 {
		return e.Msg
	}
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

// LoadOptions configures how Agentfiles are loaded.
type LoadOptions struct {
	SkillPaths []string // Paths to search for skills
}

// ParseString parses an Agentfile from a string.
func ParseString(input string) (*Workflow, error) {
	p := newParser(NewLexer(input))
	return p.Parse()
}

// LoadFile loads and parses an Agentfile from the given path,
// resolving all FROM paths relative to the Agentfile location.
func LoadFile(path string) (*Workflow, error) {
	return LoadFileWithOptions(path, LoadOptions{})
}

// LoadFileWithOptions loads an Agentfile with custom options.
func LoadFileWithOptions(path string, opts LoadOptions) (*Workflow, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read Agentfile: %w", err)
	}

	wf, err := ParseString(string(content))
	if err != nil {
		return nil, err
	}

	// Resolve FROM paths relative to Agentfile directory
	baseDir := filepath.Dir(path)
	wf.BaseDir = baseDir

	// Load agents with smart FROM resolution
	for i := range wf.Agents {
		agent := &wf.Agents[i]
		// Skip if agent has inline prompt (no FROM path)
		if agent.FromPath == "" && agent.Prompt != "" {
			continue
		}
		if agent.FromPath != "" {
			if err := resolveAgentFrom(agent, baseDir, opts.SkillPaths); err != nil {
				return nil, fmt.Errorf("line %d: %w", agent.Line, err)
			}
		}
	}

	// Load goal prompts from FROM paths
	for i := range wf.Goals {
		goal := &wf.Goals[i]
		if goal.FromPath != "" {
			goalPath := filepath.Join(baseDir, goal.FromPath)
			goalContent, err := os.ReadFile(goalPath)
			if err != nil {
				return nil, fmt.Errorf("line %d: failed to load goal prompt %q: %w",
					goal.Line, goal.FromPath, err)
			}
			goal.Outcome = string(goalContent)
		}
	}

	// Validate the loaded workflow
	if err := Validate(wf); err != nil {
		return nil, err
	}

	return wf, nil
}

// resolveAgentFrom resolves an agent's FROM path using smart resolution:
// 1. File exists + ends with .md → Load as prompt
// 2. Directory exists + has SKILL.md → Load as skill
// 3. Directory exists, no SKILL.md → Error
// 4. Neither exists → Search skill paths
// 5. Still not found → Error
func resolveAgentFrom(agent *Agent, baseDir string, skillPaths []string) error {
	target := agent.FromPath

	// Try as relative path first
	fullPath := filepath.Join(baseDir, target)

	// Check if it's a file
	if info, err := os.Stat(fullPath); err == nil {
		if !info.IsDir() {
			// It's a file - load as prompt
			if !strings.HasSuffix(target, ".md") {
				return fmt.Errorf("agent prompt file must be .md: %s", target)
			}
			content, err := os.ReadFile(fullPath)
			if err != nil {
				return fmt.Errorf("failed to load agent prompt %q: %w", target, err)
			}
			agent.Prompt = string(content)
			agent.IsSkill = false
			return nil
		}

		// It's a directory - must be a skill
		return loadAgentFromSkillDir(agent, fullPath)
	}

	// Path doesn't exist relative to baseDir - search skill paths
	for _, skillPath := range skillPaths {
		// Expand ~ if present
		if strings.HasPrefix(skillPath, "~") {
			home, _ := os.UserHomeDir()
			skillPath = filepath.Join(home, skillPath[1:])
		}

		candidatePath := filepath.Join(skillPath, target)
		if info, err := os.Stat(candidatePath); err == nil && info.IsDir() {
			return loadAgentFromSkillDir(agent, candidatePath)
		}
	}

	return fmt.Errorf("agent not found: %s (checked relative path and skill paths)", target)
}

// loadAgentFromSkillDir loads an agent from a skill directory.
func loadAgentFromSkillDir(agent *Agent, skillDir string) error {
	skillMdPath := filepath.Join(skillDir, "SKILL.md")

	if _, err := os.Stat(skillMdPath); os.IsNotExist(err) {
		return fmt.Errorf("directory %s is not a valid skill (missing SKILL.md)", skillDir)
	}

	// Load the skill
	skill, err := skills.Load(skillDir)
	if err != nil {
		return fmt.Errorf("failed to load skill from %s: %w", skillDir, err)
	}

	// Build prompt from skill
	var prompt strings.Builder
	prompt.WriteString(skill.Description)
	prompt.WriteString("\n\n")
	prompt.WriteString(skill.Instructions)

	// Add available scripts info
	scripts, _ := skill.ListScripts()
	if len(scripts) > 0 {
		prompt.WriteString("\n\n## Available Scripts\n")
		for _, s := range scripts {
			prompt.WriteString(fmt.Sprintf("- %s\n", s))
		}
	}

	agent.Prompt = prompt.String()
	agent.IsSkill = true
	agent.SkillDir = skillDir

	return nil
}

// Validate validates the workflow AST, returning errors.Join of any
// ValidationError found. Use errors.As to recover individual failures.
func Validate(wf *Workflow) error {
	var errs []error

	// R1.3.6: Verify NAME is specified
	if wf.Name == "" {
		errs = append(errs, &ValidationError{Msg: "NAME is required"})
	}

	// R1.3.7: Verify at least one RUN step exists
	if len(wf.Steps) == 0 {
		errs = append(errs, &ValidationError{Msg: "at least one RUN step is required"})
	}

	// Build lookup maps
	definedAgents := make(map[string]bool)
	for _, agent := range wf.Agents {
		definedAgents[agent.Name] = true
	}

	definedGoals := make(map[string]bool)
	for _, goal := range wf.Goals {
		definedGoals[goal.Name] = true
	}

	// R1.3.1: Verify all agents referenced in USING clauses are defined
	for _, goal := range wf.Goals {
		for _, agentName := range goal.UsingAgent {
			if !definedAgents[agentName] {
				errs = append(errs, &ValidationError{
					Line: goal.Line,
					Msg:  fmt.Sprintf("undefined agent %q in USING clause", agentName),
				})
			}
		}
	}

	// R1.3.2: Verify all goals referenced in RUN steps are defined
	for _, step := range wf.Steps {
		for _, goalName := range step.UsingGoals {
			if !definedGoals[goalName] {
				errs = append(errs, &ValidationError{
					Line: step.Line,
					Msg:  fmt.Sprintf("undefined goal %q in %s step", goalName, step.Type),
				})
			}
		}
	}

	// Supervision downgrade validation: SUPERVISED HUMAN cannot be downgraded
	// Per docs/execution/03-supervision-modes.md: "Goals can escalate or downgrade
	// supervision, except when global is SUPERVISED HUMAN — then downgrading is an error."
	if wf.Supervised && wf.HumanOnly {
		for _, goal := range wf.Goals {
			if goal.Supervision == SupervisionDisabled {
				errs = append(errs, &ValidationError{
					Line: goal.Line,
					Msg:  fmt.Sprintf("goal %q cannot be UNSUPERVISED when global is SUPERVISED HUMAN", goal.Name),
				})
			}
		}
		for _, agent := range wf.Agents {
			if agent.Supervision == SupervisionDisabled {
				errs = append(errs, &ValidationError{
					Line: agent.Line,
					Msg:  fmt.Sprintf("agent %q cannot be UNSUPERVISED when global is SUPERVISED HUMAN", agent.Name),
				})
			}
		}
		for _, step := range wf.Steps {
			if step.Supervision == SupervisionDisabled {
				errs = append(errs, &ValidationError{
					Line: step.Line,
					Msg:  fmt.Sprintf("step %q cannot be UNSUPERVISED when global is SUPERVISED HUMAN", step.Name),
				})
			}
		}
	}

	return errors.Join(errs...)
}
