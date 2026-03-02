# 07 — Translation Bureau

Four translators, each handling a different language. Submit the same text to all four for parallel translation.

## Agents

- **translator-es** — English → Spanish
- **translator-de** — English → German
- **translator-ja** — English → Japanese
- **translator-fr** — English → French

## Usage

```bash
swarm up
# Submit same text to all translators
swarm submit translate-es "The quick brown fox jumps over the lazy dog"
swarm submit translate-de "The quick brown fox jumps over the lazy dog"
swarm submit translate-ja "The quick brown fox jumps over the lazy dog"
swarm submit translate-fr "The quick brown fox jumps over the lazy dog"
swarm history
swarm down
```

## Pattern: Fan-Out by Specialization

```
         ┌─ translator-es → Spanish
         ├─ translator-de → German
submit ──┤
         ├─ translator-ja → Japanese
         └─ translator-fr → French
```

Unlike 04-parallel-research (same capability, load balanced), here each agent has a unique capability. All four run simultaneously on the same input.
