# Examples

This directory contains example Agentfiles demonstrating various workflows.

## Setup

```bash
# Build the agent
cd ../src
go build -o agent ./cmd/agent

# Set your API keys
export ANTHROPIC_API_KEY="your-key"
export OPENAI_API_KEY="your-key"  # Only if using code-generation profile

# Run from examples directory
cd ../examples
../src/agent run 01-hello-world.agent --config agent.json
```

## Capability Profiles

The `agent.json` config defines these profiles:

| Profile | Model | Use Case |
|---------|-------|----------|
| *(default)* | claude-sonnet | General purpose |
| `reasoning-heavy` | claude-opus | Complex analysis, security audits |
| `fast` | claude-haiku | Quick tasks, debates |
| `code-generation` | gpt-4o | Code writing (OpenAI) |
| `creative` | claude-sonnet (8k tokens) | Long-form creative writing |

Agents declare requirements:
```
AGENT critic FROM agents/critic.md REQUIRES "reasoning-heavy"
```

## Supervision

Enable drift detection and course correction with the `SUPERVISED` keyword:

```
# Global supervision - applies to all steps
SUPERVISED
NAME my-workflow
GOAL analyze "Analyze data"
```

```
# Per-step supervision
GOAL deploy "Deploy to production" SUPERVISED HUMAN
GOAL cleanup "Quick cleanup" UNSUPERVISED
```

| Mode | Behavior |
|------|----------|
| `SUPERVISED` | Auto-corrects drift, falls back to autonomous decisions |
| `SUPERVISED HUMAN` | Requires human approval, hard-fails if unavailable |
| `UNSUPERVISED` | Skips supervision for trusted/fast operations |

See `39-supervision/` for detailed examples.

## Examples

| File | Description | Key Features |
|------|-------------|--------------|
| **Basic** |
| `01-hello-world.agent` | Simplest workflow | Single goal |
| `02-research-pipeline.agent` | Multi-step research | Sequential goals |
| **Programming** |
| `03-code-review.agent` | Code analysis | glob, read tools |
| `04-bug-fixer.agent` | Fix until tests pass | LOOP, bash tool |
| `05-doc-generator.agent` | Generate docs | File I/O |
| `11-api-builder.agent` | Generate REST API | Code generation |
| `12-test-writer.agent` | Generate tests | Code analysis |
| `13-refactor.agent` | Iterative refactoring | LOOP, edit tool |
| `18-git-analyzer.agent` | Git history insights | bash tool |
| `21-security-audit.agent` | Security review | `reasoning-heavy` profile |
| `22-schema-designer.agent` | Database design | DDL generation |
| `23-changelog.agent` | Generate changelogs | Git, markdown |
| `25-dep-audit.agent` | Dependency analysis | Package ecosystems |
| **Writing** |
| `06-essay-writer.agent` | Academic essays | Multi-step drafting |
| `08-meeting-processor.agent` | Meeting notes | File I/O |
| `09-story-generator.agent` | Creative fiction | `creative` + `reasoning-heavy` profiles |
| `20-translator.agent` | Context-aware translation | Cultural awareness |
| **Planning** |
| `07-recipe-creator.agent` | Recipe from ingredients | Creative generation |
| `14-learning-planner.agent` | Learning paths | Structured output |
| `15-travel-planner.agent` | Travel itineraries | Research + planning |
| `16-fitness-designer.agent` | Workout programs | Domain expertise |
| `19-resume-tailor.agent` | Resume customization | Job matching |
| `24-interview-prep.agent` | Interview coaching | Q&A generation |
| **Analysis** |
| `10-decision-analyzer.agent` | Decision analysis | `fast` + `reasoning-heavy` profiles |
| `17-debate-simulator.agent` | Debate simulation | `fast` profile, LOOP |
| **Supervision** |
| `39-supervision/` | Drift detection examples | `SUPERVISED`, `SUPERVISED HUMAN` |

## Agent Personas

The `agents/` directory contains reusable agent personas:

- `creative-writer.md` - Imaginative storyteller
- `editor.md` - Critical reviewer
- `optimist.md` - Opportunity-focused strategist
- `devils-advocate.md` - Risk identifier
- `security-auditor.md` - Vulnerability hunter

## Running Examples

```bash
# Simple example
../src/agent run 01-hello-world.agent --config agent.json

# With custom input
../src/agent run 02-research-pipeline.agent --config agent.json --input topic="quantum computing"

# Code review on a directory
../src/agent run 03-code-review.agent --config agent.json --input path="../src/internal/executor"

# Iterative bug fixing
../src/agent run 04-bug-fixer.agent --config agent.json --input test_command="go test ./..."

# Multi-agent with profiles (uses claude-opus for critic, claude-sonnet for writer)
../src/agent run 09-story-generator.agent --config agent.json --input theme="redemption"

# Debate with fast models
../src/agent run 17-debate-simulator.agent --config agent.json --input topic="AI regulation"

# Validate without running
../src/agent validate 09-story-generator.agent

# Inspect workflow structure
../src/agent inspect 17-debate-simulator.agent
```
