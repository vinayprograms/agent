# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /build

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Build with version info
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=1 go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o /agent ./cmd/agent
RUN CGO_ENABLED=1 go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o /replay ./cmd/replay

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache git bash ca-certificates curl

# Create non-root user for security
RUN adduser -D -h /home/agent agent
USER agent

COPY --from=builder /agent /usr/local/bin/agent
COPY --from=builder /replay /usr/local/bin/agent-replay

# Default config directory
ENV AGENT_CONFIG_DIR=/home/agent/.config/grid
RUN mkdir -p /home/agent/.config/grid /home/agent/.local/share/agent

WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/agent"]
CMD ["--help"]
