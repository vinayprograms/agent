# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /build

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=1 go build -o /agent ./cmd/agent

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache git bash ca-certificates

COPY --from=builder /agent /usr/local/bin/agent

WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/agent"]
