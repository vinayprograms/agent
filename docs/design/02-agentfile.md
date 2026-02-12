# Chapter 2: Agentfile DSL

## Overview

Flat, declarative workflow definition. One instruction per line, no indentation, no nesting.

## Keywords

| Keyword | Purpose |
|---------|---------|
| NAME | Workflow identifier |
| INPUT | Declare input parameter (with optional DEFAULT) |
| AGENT | Define agent from prompt, file, or skill |
| GOAL | Define goal with description |
| RUN | Execute goals sequentially |
| LOOP | Execute goals repeatedly within a limit |
| FROM | Load content from path |
| USING | Specify which agents/goals to use |
| WITHIN | Set iteration limit for LOOP |
| DEFAULT | Default value for INPUT |
| REQUIRES | Capability profile requirement |
| SUPERVISED | Enable execution supervision |
| HUMAN | Require human approval (with SUPERVISED) |
| UNSUPERVISED | Disable supervision |
| SECURITY | Set security mode |

## Syntax

```
# Comments start with #

NAME workflow-name

INPUT required_param
INPUT optional_param DEFAULT "value"

AGENT name FROM path/to/prompt.md
AGENT name FROM skill-name
AGENT name FROM path/to/skill REQUIRES "profile"
AGENT name "Inline prompt"

GOAL name "Description with $variables"
GOAL name "Description" -> output1, output2
GOAL name "Description" USING agent1, agent2

RUN step_name USING goal1, goal2
LOOP step_name USING goal1 WITHIN 5
LOOP step_name USING goal1 WITHIN $max_iter
```

## Strings

### Single-line strings

Use double quotes for simple, single-line descriptions:

```
GOAL analyze "Analyze the code and find issues"
```

Escape sequences: `\n` (newline), `\t` (tab), `\\` (backslash), `\"` (quote)

### Multi-line strings (triple quotes)

Use triple quotes `"""` for medium-complexity descriptions:

```
GOAL analyze """
Analyze the provided code for:
1. Security vulnerabilities
2. Performance issues
3. Code style violations
"""
```

Triple-quoted strings:
- Preserve newlines exactly as written
- Optional newline after opening `"""` is stripped
- Trailing newline before closing `"""` is stripped
- No escape sequence processing needed

### External markdown files (recommended for complex prompts)

For complex, reusable, or lengthy prompts, use external markdown files:

```
AGENT analyzer FROM prompts/security-analyzer.md
GOAL analyze "Run security analysis" USING analyzer
```

**When to use each:**

| Complexity | Approach | Example |
|------------|----------|---------|
| Simple (1-2 sentences) | Inline string | `GOAL x "Do the thing"` |
| Medium (list, few paragraphs) | Triple quotes | `GOAL x """..."""` |
| Complex (detailed instructions) | Markdown file | `AGENT x FROM prompts/x.md` |

Benefits of markdown files:
- Syntax highlighting in editors
- Reusable across workflows
- Easier to maintain and version
- Keeps Agentfiles concise

## Variable Interpolation

Use `$variable` to reference inputs and outputs:

```
INPUT topic DEFAULT "Go programming"
GOAL research "Research $topic and list 3 key facts"
```

Variables work in both single-line and multi-line strings:

```
GOAL analyze """
Analyze $file_path for:
- Security issues
- Performance problems
"""
```

## Structured Output

Use `->` to declare output fields:

```
GOAL research "Research $topic" -> findings, sources, confidence
GOAL report "Write report using $findings" -> summary
```

The LLM returns JSON with those fields. Fields become variables for subsequent goals.

## Multi-Agent Goals

When a goal uses multiple agents, they run in parallel:

```
AGENT researcher "Research $topic" -> findings
AGENT critic "Find biases in $topic" -> issues

GOAL analyze "Analyze $topic" -> summary USING researcher, critic
```

An implicit synthesizer transforms their outputs into the goal's fields.

## AGENT FROM Resolution

| FROM Value | Resolution |
|------------|------------|
| `agents/critic.md` | File path → loads as prompt |
| `skills/code-review` | Directory with SKILL.md → loads as skill |
| `testing` | Name → searches skills.paths |

Resolution order:
1. Check if path exists relative to Agentfile
2. If file → load as prompt (must be .md)
3. If directory → must have SKILL.md
4. If not found → search configured skills.paths
5. If still not found → error

## Capability Profiles

Agents can require specific capabilities:

```
AGENT critic FROM agents/critic.md REQUIRES "reasoning-heavy"
```

Profiles are defined in agent.toml:

```toml
[profiles.reasoning-heavy]
model = "claude-opus-4-20250514"

[profiles.fast]
model = "gpt-4o-mini"
```

Benefits:
- Workflow declares intent (what capability)
- Config controls implementation (which model)
- Same Agentfile works in different environments

## Supervision

Global (at top of file):

```
SUPERVISED
NAME my-workflow
GOAL step1 "First goal"
```

Per-goal (at end of line):

```
GOAL deploy "Deploy to production" SUPERVISED HUMAN
GOAL cleanup "Quick cleanup" UNSUPERVISED
```

See [Supervision Modes](../execution/03-supervision-modes.md).

## Security Mode

```
SECURITY paranoid
NAME high-security-workflow
```

See [Security Modes](../security/07-security-modes.md).

---

Next: [LLM Integration](03-llm.md)
