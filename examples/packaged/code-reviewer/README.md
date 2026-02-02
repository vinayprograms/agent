# Code Reviewer Agent

A multi-agent code review system that uses different LLM profiles for different tasks:
- **reviewer**: Uses `reasoning-heavy` profile for deep analysis
- **style-checker**: Uses `fast` profile for quick style checks

## Usage

```bash
# Pack
agent pack ./code-reviewer -o code-reviewer.agent

# Run (requires config.json with profile mappings)
agent run code-reviewer.agent --input code_path=./src/main.go
```

## Required Profiles

Configure in your `config.json`:

```json
{
  "profiles": {
    "reasoning-heavy": {
      "model": "claude-opus-4-20250514",
      "thinking": "high"
    },
    "fast": {
      "model": "claude-sonnet-4-20250514"
    }
  }
}
```
