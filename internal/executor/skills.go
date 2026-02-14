// Skill handling functions for the executor.
package executor

import (
	"fmt"
	"regexp"

	"github.com/vinayprograms/agent/internal/skills"
)

// checkSkillActivation checks if content triggers skill activation.
func (e *Executor) checkSkillActivation(content string) *skills.Skill {
	re := regexp.MustCompile(`\[use-skill:([a-z0-9-]+)\]`)
	matches := re.FindStringSubmatch(content)
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
			if e.OnSkillLoaded != nil {
				e.OnSkillLoaded(skillName)
			}
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
