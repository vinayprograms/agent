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
| CONVERGE | Define convergence goal (iterative refinement) |
| RUN | Execute goals sequentially |
| FROM | Load content from path |
| USING | Specify which agents/goals to use |
| WITHIN | Set iteration limit for CONVERGE |
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

CONVERGE refine "Refine until clean" WITHIN 10
CONVERGE polish "Polish the output" -> result WITHIN $max_iter
CONVERGE improve "Improve with feedback" -> result USING critic WITHIN 5
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

## Convergence Goals

CONVERGE goals implement iterative refinement (the "Ralph Wiggum loop" pattern). The agent refines its output repeatedly until it converges on a stable result.

```
CONVERGE refine "Refine the code until it's clean" WITHIN 10
```

### How it works

1. Agent executes the goal and produces output
2. Output is fed back as context for the next iteration
3. Agent sees all previous iterations in `<convergence-history>` tags
4. Process repeats until the goal converges (see below) or hits the WITHIN limit
5. Final output is the last substantive iteration (not the convergence signal itself)

### Deciding convergence

The agent that is deciding convergence (the single agent for a plain CONVERGE
goal, or the last agent in a `USING` pipeline — see below) reports
convergence by calling a `converged(reason)` tool, not by writing a marker
word in its reply. This is the primary channel, and the reason it gives is
logged on the goal's `goal_end` session event, so "why did this converge?"
is answerable from the logs without re-running the goal.

Not every provider can force a model to call a specific tool (notably Ollama
Cloud, which offers tools but can't pin one). For that case there is a
lenient prose fallback: if the agent's last non-empty line, once whitespace,
markdown fences, and trailing punctuation are stripped, reads `CONVERGED`
(case-insensitive), the goal is treated as converged anyway. The word
appearing mid-paragraph does not count — only a marker on its own trailing
line does. Authors don't need to do anything for this: both channels are
always available, and the runtime picks whichever the model actually used.

The same tool call also carries any `-> outputs` fields the goal declares,
so a CONVERGE goal reports its structured outputs in the same call that
reports convergence, rather than needing the outputs scraped separately out
of prose (see `-> outputs` below).

### CONVERGE with USING is a pipeline

Unlike `GOAL ... USING`, which fans agents out in **parallel** and
synthesizes their outputs, `CONVERGE ... USING` runs its agents
**sequentially**, once per iteration: agent `a` runs first, then agent `b`
runs and sees `a`'s output as context, and so on. Because the pipeline is
sequential, there is nothing to synthesize — the **last agent's output is
the goal's output** directly.

Only the **last agent in the pipeline decides convergence** (via the
`converged` tool or the lenient fallback, as above). Earlier agents in the
pipeline are not offered the tool at all, and their prompts carry no
convergence instruction — an agent that isn't the one deciding shouldn't be
told to call a decision it doesn't have. A typical use is a
generator-then-critic pipeline where the critic (last agent) is the one
that ultimately judges the work done:

```
CONVERGE polish "Refine the code until it passes review" -> clean_code USING drafter, critic WITHIN 5
```

Each iteration: `drafter` produces a draft, `critic` reviews it (seeing the
draft as context) and either calls `converged` when satisfied or returns
feedback that becomes the next iteration's starting point. `drafter`'s
output within an iteration is not itself the goal's output — only the last
agent's (`critic`'s) output is.

### Syntax

```
CONVERGE <name> "<description>" [-> outputs] [USING agents] WITHIN <limit|$var> [SUPERVISED]

The clauses after the description (`-> outputs`, `USING`, `WITHIN`, and the
supervision modifier) may appear in any order; each at most once. The same
holds for `GOAL`.
```

### Key features

- **Same capabilities as GOAL**: tools, USING, spawn_agents, supervision all work
- **USING is sequential, not fan-out**: see "CONVERGE with USING is a pipeline" above
- **Convergence is decided by one agent**: the single agent, or the last in the pipeline — via the `converged` tool, with a lenient prose fallback
- **Safety limit**: WITHIN prevents infinite loops
- **Limit is hidden**: The LLM never sees the max iteration count (prevents gaming)
- **Graceful degradation**: If limit is hit, returns last output with a warning
- **Side effects persist**: File changes from all iterations are kept

### Example: Code refinement

```
NAME code-polish

AGENT drafter "You write and revise code."
AGENT critic "You are a code critic. Find issues, or call converged when there are none left."
CONVERGE polish "Refine the code until it passes review" -> clean_code USING drafter, critic WITHIN 5

RUN main USING polish
```

Each iteration:
1. `drafter` produces (or revises) the code
2. `critic` reviews it, seeing `drafter`'s output as context (sequential pipeline)
3. If issues remain, `critic` returns feedback — that's the iteration's output, and the next iteration starts from it
4. When `critic` judges the code clean, it calls the `converged` tool (or, as a fallback, writes `CONVERGED` as the trailing line of its reply)

### Warning on non-convergence

If the WITHIN limit is reached without convergence, replay shows:
```
⚠ WARNING: Goal "polish" did not converge within limit (used all iterations)
```

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

## Efficiency

Agents should be frugal with tool calls, web searches, and output length. See [Efficiency Guidelines](07-efficiency.md) for:

- Minimal tool usage
- Web search discipline (0-5 searches typical)
- Concise, actionable output
- CONVERGE iteration targets

**In goal descriptions**, be specific to constrain scope:

```
# Vague (invites runaway)
GOAL analyze "Analyze the codebase"

# Specific (constrains effort)  
GOAL analyze "Find SQL injection vulnerabilities in handler files"
```

---

Next: [LLM Integration](03-llm.md)
