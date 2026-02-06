# Chapter 2: Agentfile DSL

## Overview

The Agentfile is a flat, declarative workflow definition language. Inspired by Dockerfile simplicity — one instruction per line, no indentation, no nesting.

## Keywords

| Keyword | Purpose |
|---------|---------|
| NAME | Workflow identifier |
| INPUT | Declare required input parameter |
| AGENT | Define a reusable agent with prompt |
| GOAL | Define a goal for agent to achieve |
| RUN | Execute goals using agents |
| LOOP | Repeat goals within a limit |
| FROM | Load content from file path |
| USING | Specify which agents/goals to use |
| WITHIN | Set iteration limit for LOOP |
| DEFAULT | Default value for INPUT |
| REQUIRES | Capability requirement for AGENT |
| SUPERVISED | Enable execution supervision |
| HUMAN | Require human approval (with SUPERVISED) |
| UNSUPERVISED | Disable supervision for goal |
| SECURITY | Set security mode |

## Syntax Rules

1. One instruction per line
2. No indentation or nesting
3. Keywords are UPPERCASE
4. Strings use double quotes
5. Variables use `{name}` for interpolation
6. Comments start with `#`

## Examples

### Basic Workflow

```
NAME analyze-logs
INPUT log_file

GOAL analyze "Analyze {log_file} and summarize errors"
```

### With Agents

```
NAME code-review
INPUT repo_path

AGENT reviewer "You are a code reviewer. Be thorough but concise."
GOAL review "Review code in {repo_path}" USING reviewer
```

### With Supervision

```
NAME deploy-service
INPUT version
INPUT environment

SUPERVISED

GOAL build "Build version {version}"
GOAL test "Run test suite"
GOAL deploy "Deploy to {environment}" SUPERVISED HUMAN
```

### With Security Mode

```
NAME public-api-agent
SECURITY paranoid

GOAL process "Process user request from API"
```

### With Structured Output

```
NAME extract-data
INPUT document

GOAL extract "Extract entities from {document}" -> entities, summary
```

### Loading From Files

```
NAME complex-workflow

AGENT analyst FROM prompts/analyst.md
GOAL analyze "Analyze quarterly data" USING analyst
```

### Loops

```
NAME iterative-refinement
INPUT draft

LOOP refine USING improve, evaluate WITHIN 5
```

## Parsing

The lexer tokenizes the Agentfile, then the parser builds an AST:

| AST Node | Fields |
|----------|--------|
| Workflow | Name, Inputs, Agents, Goals, Steps, Supervised, HumanOnly |
| Input | Name, Default, Line |
| Agent | Name, Prompt, FromPath, Outputs, Requires, Supervised, HumanOnly |
| Goal | Name, Outcome, FromPath, Outputs, UsingAgent, Supervised, HumanOnly |
| Step | Type (RUN/LOOP), Name, UsingGoals, WithinLimit, Supervised, HumanOnly |

## Supervision Modifiers

| Position | Scope |
|----------|-------|
| Top of file (before NAME) | Global default for all goals |
| End of GOAL line | Override for that specific goal |

```
SUPERVISED                           # Global
NAME my-workflow
GOAL step1 "First goal"              # Inherits SUPERVISED
GOAL step2 "Second goal" UNSUPERVISED # Override to unsupervised
GOAL step3 "Third goal" SUPERVISED HUMAN  # Escalate to human
```

## Security Mode

Set security mode at top of file:

```
SECURITY paranoid
NAME high-security-workflow
GOAL process "Handle untrusted input"
```

See [Security Modes](../security/07-security-modes.md) for details.

---

Next: [LLM Integration](03-llm.md)
