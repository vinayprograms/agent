---
name: testing
description: Write and manage unit tests, integration tests, and test suites. Use when user asks about testing, test coverage, or quality assurance.
license: MIT
metadata:
  author: vinayprograms
  version: "1.0"
---

# Testing Instructions

## Test Structure

### Unit Tests
- Test one function/method per test
- Use descriptive test names
- Follow Arrange-Act-Assert pattern
- Mock external dependencies

### Integration Tests
- Test component interactions
- Use realistic test data
- Clean up after tests

## Naming Conventions

```
Test<Function>_<Scenario>_<ExpectedResult>

Examples:
- TestParseConfig_ValidJSON_ReturnsConfig
- TestUserCreate_DuplicateEmail_ReturnsError
```

## Coverage Guidelines

- Aim for 80%+ coverage on critical paths
- 100% coverage on security-sensitive code
- Don't test trivial getters/setters

## Test Categories

1. **Happy path**: Normal successful operations
2. **Edge cases**: Boundaries, empty inputs, max values
3. **Error handling**: Invalid inputs, failures
4. **Security**: Input validation, auth checks

## Language-Specific

### Go
```go
func TestExample(t *testing.T) {
    // Arrange
    input := "test"
    
    // Act
    result := Process(input)
    
    // Assert
    if result != expected {
        t.Errorf("got %v, want %v", result, expected)
    }
}
```

### Python
```python
def test_example():
    # Arrange
    input = "test"
    
    # Act
    result = process(input)
    
    # Assert
    assert result == expected
```
