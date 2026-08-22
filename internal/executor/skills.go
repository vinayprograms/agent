package executor

import (
	"context"
	"fmt"
	"regexp"

	"github.com/vinayprograms/agent/internal/hooks"
	"github.com/vinayprograms/agent/internal/skills"
)

// skillActivation matches the [use-skill:name] marker a model emits to
// pull in a skill.
var skillActivation = regexp.MustCompile(`\[use-skill:([a-z0-9-]+)\]`)

// checkSkillActivation checks if content triggers skill activation.
func (e *Executor) checkSkillActivation(ctx context.Context, content string) *skills.Skill {
	matches := skillActivation.FindStringSubmatch(content)
	if len(matches) < 2 {
		return nil
	}

	skillName := matches[1]

	// Check if already loaded
	if skill, ok := e.loadedSkills[skillName]; ok {
		return skill
	}

	// Find and load skill
	for _, ref := range e.skillRefs {
		if ref.Name == skillName {
			skill, err := skills.Load(ref.Path)
			if err != nil {
				return nil
			}
			e.loadedSkills[skillName] = skill
			e.hooks.Fire(ctx, hooks.SkillLoaded, map[string]any{"name": skillName})
			return skill
		}
	}

	return nil
}

// getSkillContext returns the context to inject for a skill.
func (e *Executor) getSkillContext(skill *skills.Skill) string {
	context := fmt.Sprintf("# Skill: %s\n\n%s", skill.Name, skill.Instructions)

	// List available scripts
	scripts, _ := skill.ListScripts()
	if len(scripts) > 0 {
		context += "\n\n## Available Scripts\n"
		for _, s := range scripts {
			context += fmt.Sprintf("- %s\n", s)
		}
	}

	return context
}
