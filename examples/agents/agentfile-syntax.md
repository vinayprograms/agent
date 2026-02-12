# Agentfile Syntax Reference

You are generating an Agentfile - a flat, declarative workflow definition. Follow this syntax exactly.

## Basic Structure

```
# Comments start with #

NAME workflow-name

INPUT required_param
INPUT optional_param DEFAULT "value"

GOAL step1 "Description with $variables"
GOAL step2 "Another step"

RUN main USING step1, step2
```

## Keywords

| Keyword | Purpose |
|---------|---------|
| NAME | Workflow identifier (required, lowercase-with-hyphens) |
| INPUT | Declare input parameter |
| DEFAULT | Default value for INPUT |
| AGENT | Define sub-agent from prompt file or inline |
| GOAL | Define a step with description |
| RUN | Execute goals sequentially |
| LOOP | Execute goals repeatedly |
| USING | Specify which goals/agents to use |
| WITHIN | Set iteration limit for LOOP |
| REQUIRES | Capability profile requirement |
| SUPERVISED | Enable execution supervision |
| UNSUPERVISED | Disable supervision |

## Strings

### Single-line strings

Use double quotes for simple descriptions:

```
GOAL analyze "Analyze the code and find issues"
```

Escape sequences: `\n`, `\t`, `\\`, `\"`

### Multi-line strings (triple quotes)

Use triple quotes `"""` for longer descriptions:

```
GOAL analyze """
Analyze the provided code for:
1. Security vulnerabilities
2. Performance issues
3. Code style violations

Use the read tool to examine files.
"""
```

Triple-quoted strings preserve newlines exactly. Use them when goal descriptions need multiple lines, lists, or detailed instructions.

## Variable Interpolation

Use `$variable` to reference inputs:

```
INPUT topic DEFAULT "security"
GOAL research "Research $topic and summarize findings"
```

Variables work in both single-line and multi-line strings.

## Sequential Execution (RUN)

Goals run one after another:

```
GOAL step1 "First step"
GOAL step2 "Second step uses output from step1"
GOAL step3 "Final step"

RUN pipeline USING step1, step2, step3
```

## Iterative Execution (LOOP)

Goals repeat until success or limit reached:

```
GOAL attempt "Try to fix the issue"
GOAL verify "Check if fixed"

LOOP fix-cycle USING attempt, verify WITHIN 5
```

Use LOOP when:
- Fixing bugs until tests pass
- Refining content until quality threshold met
- Retrying operations that may fail

## Structured Output

Declare output fields with `->`:

```
GOAL analyze "Analyze the code" -> issues, recommendations, severity
GOAL report "Generate report from $issues and $recommendations"
```

The LLM returns JSON with those fields. Fields become variables for subsequent goals.

## Sub-Agents (AGENT)

Define specialized agents for complex workflows:

```
# From markdown file
AGENT researcher FROM agents/researcher.md

# Inline prompt
AGENT critic "You are a critical reviewer. Find flaws and gaps."

# With capability profile
AGENT deep-thinker FROM agents/analyst.md REQUIRES "reasoning-heavy"
```

Use agents in goals:

```
GOAL review "Review the document" USING researcher, critic
```

Multiple agents in USING run in parallel, outputs are synthesized.

## Capability Profiles

Request specific model capabilities:

```
AGENT analyzer FROM agents/analyzer.md REQUIRES "reasoning-heavy"
AGENT formatter FROM agents/formatter.md REQUIRES "fast"
```

Common profiles: `reasoning-heavy`, `fast`, `code-heavy`

## Supervision

For critical steps requiring oversight:

```
GOAL deploy "Deploy to production" SUPERVISED
GOAL dangerous-op "Destructive operation" SUPERVISED HUMAN
```

## Tools Available

Reference these in goal descriptions:
- `bash` - Run shell commands
- `read` - Read file contents
- `write` - Create/overwrite files
- `edit` - Make precise edits to files
- `glob` - Find files by pattern

## Examples

### Simple Pipeline
```
NAME doc-generator
INPUT source_dir

GOAL scan "Use glob to find all .go files in $source_dir"
GOAL analyze "Read each file and extract function signatures"
GOAL generate "Write documentation to README.md"

RUN main USING scan, analyze, generate
```

### Bug Fix Loop
```
NAME bug-fixer
INPUT test_command DEFAULT "go test ./..."

GOAL diagnose "Run $test_command, analyze failures"
GOAL fix "Apply minimal fixes using edit tool"
GOAL verify "Run $test_command again"

LOOP fix-cycle USING diagnose, fix, verify WITHIN 5
```

### Multi-Agent Review
```
NAME code-review
INPUT file_path

AGENT security-reviewer "Focus on security vulnerabilities"
AGENT perf-reviewer "Focus on performance issues"

GOAL review "Review $file_path" USING security-reviewer, perf-reviewer
GOAL synthesize "Combine findings into actionable report"

RUN main USING review, synthesize
```

## Rules

1. One instruction per line, no indentation
2. NAME must be first non-comment line
3. INPUTs must come before GOALs
4. AGENTs must be defined before use in USING
5. Goal names: lowercase-with-hyphens
6. Goal descriptions: clear, actionable, mention tools when relevant
7. Keep workflows focused - one clear purpose
8. Match complexity to the problem
