# Example Test Data

This directory contains input and expected output files for automated testing of the headless-agent examples.

## Structure

For each example `XX-name.agent`, there are two test files:

- `XX-name.input.json` - Input parameters to run the example
- `XX-name.expected.json` - Validation criteria for the output

## Input File Format

```json
{
  "inputs": {
    "param1": "value1",
    "param2": "value2"
  },
  "setup": {
    "description": "Optional setup instructions",
    "files": {
      "path/to/file": "content"
    },
    "workdir": "optional working directory"
  },
  "requirements": {
    "mcp_servers": ["list", "of", "required", "servers"],
    "skills": ["required", "skills"],
    "network": true
  },
  "notes": "Optional notes for the test runner"
}
```

## Expected File Format

```json
{
  "validation_type": "file_output|output_content|code_generation|test_pass|code_improvement",
  "description": "What this example should produce",
  
  "output_files": {
    "filename.md": {
      "must_exist": true,
      "min_size_bytes": 500,
      "content_contains": ["required", "strings"],
      "required_sections": ["Section1", "Section2"],
      "format": "markdown|sql|json|text"
    }
  },
  
  "convergence": {
    "should_converge": true,
    "max_iterations": 5,
    "success_indicator": "CONVERGED or specific condition"
  },
  
  "tool_usage": {
    "required": ["tool1", "tool2"],
    "optional": ["tool3"]
  },
  
  "benchmarks": {
    "accuracy": "Description of accuracy requirements",
    "coverage": "What topics should be covered"
  },
  
  "post_validation": {
    "command": "command to run after",
    "expected_exit_code": 0,
    "expected_output": "optional expected output"
  }
}
```

## Validation Types

| Type | Description | Key Checks |
|------|-------------|------------|
| `file_output` | Example writes to specific files | File existence, content, structure |
| `output_content` | Example produces console output | Content checks, format |
| `code_generation` | Example generates code | Compiles, syntax valid |
| `test_pass` | Example should make tests pass | Exit code 0 |
| `code_improvement` | Example improves existing code | Before/after comparison |
| `test_execution` | Example is a test itself | Specific test assertions |

## Running Tests

```bash
# Run a single example with its test data
agent run examples/01-hello-world.agent \
  --input-file examples/testdata/01-hello-world.input.json

# Validate output against expected
# (Use a validation agent or script)
```

## Adding New Test Data

1. Create `XX-name.input.json` with required inputs
2. Create `XX-name.expected.json` with validation criteria
3. Add any setup files needed in the `setup.files` section
4. Document special requirements in `requirements` or `notes`
