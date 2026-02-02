# Testing Reference

## Test Frameworks by Language

| Language | Unit Test | Mocking | Coverage |
|----------|-----------|---------|----------|
| Go | testing | testify/mock | go test -cover |
| Python | pytest | unittest.mock | coverage.py |
| JavaScript | Jest | Jest mocks | Istanbul |
| Rust | cargo test | mockall | cargo tarpaulin |

## Common Patterns

### Table-Driven Tests (Go)
```go
tests := []struct {
    name    string
    input   string
    want    string
    wantErr bool
}{
    {"valid", "input", "output", false},
    {"invalid", "", "", true},
}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got, err := Function(tt.input)
        if (err != nil) != tt.wantErr {
            t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
        }
        if got != tt.want {
            t.Errorf("got %v, want %v", got, tt.want)
        }
    })
}
```
