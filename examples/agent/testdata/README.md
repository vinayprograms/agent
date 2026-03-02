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
      "path/to/file": "file content"
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

Expected files specify **what success looks like**, not just file existence:

```json
{
  "intent": "What the example is trying to accomplish",

  "success_criteria": {
    "primary": "The main thing that must be true for success",
    "secondary": "Additional success indicators"
  },

  "content_requirements": {
    "must_include": ["required content or topics"],
    "must_avoid": ["things that indicate failure"],
    "must_demonstrate": ["behaviors or qualities"]
  },

  "quality_rubric": {
    "accuracy": "How to judge correctness",
    "completeness": "How to judge coverage",
    "actionability": "How to judge usefulness"
  },

  "convergence": {
    "expected": true,
    "max_iterations": 5,
    "success_indicator": "CONVERGED or specific condition"
  },

  "verification": {
    "command": "post-run command to verify",
    "expected_exit_code": 0
  },

  "output_file": "expected output filename (if applicable)"
}
```

## Validation Philosophy

**Don't just check existence — validate intent fulfillment.**

### Quantitative Checks (Programmatic)
- Required patterns/keywords present
- Code compiles, tests pass
- Word count in range
- Required sections exist
- Post-run commands succeed

### Qualitative Checks (LLM-Judged)
- Does output address the stated intent?
- Is reasoning sound?
- Are recommendations actionable?
- Does it stay on-topic or deviate?
- Appropriate tone/audience fit?

### Semantic Checks
- Covers expected topics
- No obvious hallucinations
- Logical structure/flow

## Running Tests

```bash
# Run a single example with its test data
agent run examples/01-hello-world.agent \
  --input-file examples/testdata/01-hello-world.input.json

# Validation is done by a separate testing agent that:
# 1. Runs the example with input.json
# 2. Compares output against expected.json criteria
# 3. Reports pass/fail with specific assertions
```

## Adding New Test Data

1. Create `XX-name.input.json` with required inputs and any setup
2. Create `XX-name.expected.json` with:
   - Clear intent statement
   - Success criteria (primary and secondary)
   - Content requirements (must include/avoid)
   - Quality rubric for subjective aspects
   - Verification commands if applicable
3. Focus on **what success looks like**, not just file existence
