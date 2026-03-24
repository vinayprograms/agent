# Docker Usage

## Build

```bash
docker build -t headless-agent src/
```

## Run

```bash
docker run -it --rm \
  -v $(pwd):/workspace \
  -e ANTHROPIC_API_KEY \
  headless-agent run /workspace/Agentfile
```

## CGO-Free Build

For Docker and cross-compilation, use a CGO-free build:

```bash
CGO_ENABLED=0 go build -o agent ./cmd/agent
```

This produces a statically linked binary suitable for minimal container images (e.g., `FROM scratch` or `FROM alpine`).

---

Back to [README](../../README.md) | See also: [CLI Reference](cli-reference.md)
