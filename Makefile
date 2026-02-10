# Headless Agent Makefile

# Variables
BINARY_NAME := agent
REPLAY_BINARY := agent-replay
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Directories
BIN_DIR := bin

# Build flags
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)"
CGO_ENABLED := 1

# SQLite headers for sqlite-vec CGO bindings
CGO_CFLAGS ?= $(shell pkg-config --cflags sqlite3 2>/dev/null || echo "-I/usr/include")

# Go commands
GO := go
GOTEST := $(GO) test
GOBUILD := CGO_ENABLED=$(CGO_ENABLED) CGO_CFLAGS="$(CGO_CFLAGS)" $(GO) build $(LDFLAGS)
GOINSTALL := CGO_ENABLED=$(CGO_ENABLED) CGO_CFLAGS="$(CGO_CFLAGS)" $(GO) install $(LDFLAGS)

# Default target
.DEFAULT_GOAL := build

# =============================================================================
# Build Targets
# =============================================================================

.PHONY: build
build: ## Build agent and agent-replay binaries
	@echo "Building $(BINARY_NAME) and $(REPLAY_BINARY) $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	@$(GOBUILD) -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/agent
	@$(GOBUILD) -o $(BIN_DIR)/$(REPLAY_BINARY) ./cmd/replay
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME), $(BIN_DIR)/$(REPLAY_BINARY)"

.PHONY: build-agent
build-agent: ## Build only the agent binary
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	@$(GOBUILD) -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/agent
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)"

.PHONY: build-replay
build-replay: ## Build only the replay binary
	@echo "Building $(REPLAY_BINARY) $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	@$(GOBUILD) -o $(BIN_DIR)/$(REPLAY_BINARY) ./cmd/replay
	@echo "Built: $(BIN_DIR)/$(REPLAY_BINARY)"

.PHONY: build-static
build-static: ## Build fully static binaries (for containers)
	@echo "Building static binaries $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	@CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-static ./cmd/agent
	@CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(REPLAY_BINARY)-static ./cmd/replay
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)-static, $(BIN_DIR)/$(REPLAY_BINARY)-static"

.PHONY: build-all
build-all: ## Build for all platforms
	@echo "Building for all platforms..."
	@mkdir -p $(BIN_DIR)
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/agent
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(REPLAY_BINARY)-linux-amd64 ./cmd/replay
	@GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/agent
	@GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(REPLAY_BINARY)-linux-arm64 ./cmd/replay
	@GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/agent
	@GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(REPLAY_BINARY)-darwin-amd64 ./cmd/replay
	@GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/agent
	@GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(REPLAY_BINARY)-darwin-arm64 ./cmd/replay
	@GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/agent
	@GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BIN_DIR)/$(REPLAY_BINARY)-windows-amd64.exe ./cmd/replay
	@echo "Built binaries in $(BIN_DIR)/"

# =============================================================================
# Install Targets
# =============================================================================

.PHONY: install
install: ## Install both binaries to GOPATH/bin using go install
	@echo "Installing $(BINARY_NAME) and $(REPLAY_BINARY) $(VERSION)..."
	@$(GOINSTALL) ./cmd/agent
	@$(GOINSTALL) ./cmd/replay
	@echo "Installed to $(shell go env GOPATH)/bin"

.PHONY: uninstall
uninstall: ## Remove binaries from GOPATH/bin
	@echo "Removing binaries from GOPATH/bin..."
	@rm -f $(shell go env GOPATH)/bin/agent $(shell go env GOPATH)/bin/replay
	@echo "Uninstalled."

# =============================================================================
# Test Targets
# =============================================================================

.PHONY: test
test: ## Run all tests
	@echo "Running tests..."
	@$(GOTEST) ./...

.PHONY: test-verbose
test-verbose: ## Run tests with verbose output
	@$(GOTEST) -v ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage
	@$(GOTEST) -coverprofile=coverage.out ./...
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: test-race
test-race: ## Run tests with race detector
	@$(GOTEST) -race ./...

# =============================================================================
# Development Targets
# =============================================================================

.PHONY: fmt
fmt: ## Format code
	@$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	@$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run

.PHONY: deps
deps: ## Download dependencies
	@$(GO) mod download

.PHONY: deps-update
deps-update: ## Update dependencies
	@$(GO) get -u ./...
	@$(GO) mod tidy

.PHONY: tidy
tidy: ## Tidy go.mod
	@$(GO) mod tidy

# =============================================================================
# Docker Targets
# =============================================================================

.PHONY: docker-build
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t headless-agent:$(VERSION) \
		-t headless-agent:latest \
		.

.PHONY: docker-run
docker-run: ## Run Docker container (example)
	docker run -it --rm \
		-v $(PWD):/workspace \
		-e ANTHROPIC_API_KEY \
		headless-agent:latest --help

.PHONY: docker-push
docker-push: ## Push to Docker Hub (set DOCKER_REPO)
	docker tag headless-agent:$(VERSION) $(DOCKER_REPO)/headless-agent:$(VERSION)
	docker tag headless-agent:latest $(DOCKER_REPO)/headless-agent:latest
	docker push $(DOCKER_REPO)/headless-agent:$(VERSION)
	docker push $(DOCKER_REPO)/headless-agent:latest

# =============================================================================
# Setup & Configuration
# =============================================================================

.PHONY: setup
setup: build ## Interactive setup wizard
	@$(BIN_DIR)/$(BINARY_NAME) setup

.PHONY: setup-dev
setup-dev: ## Create development config files
	@echo "Creating development configuration..."
	@mkdir -p ~/.config/grid
	@if [ ! -f agent.toml ]; then \
		echo '[agent]' > agent.toml; \
		echo 'id = "dev-agent"' >> agent.toml; \
		echo 'workspace = "."' >> agent.toml; \
		echo '' >> agent.toml; \
		echo '[llm]' >> agent.toml; \
		echo 'model = "claude-sonnet-4-20250514"' >> agent.toml; \
		echo 'max_tokens = 4096' >> agent.toml; \
		echo '' >> agent.toml; \
		echo '[storage]' >> agent.toml; \
		echo 'path = "./data"' >> agent.toml; \
		echo "Created: agent.toml"; \
	else \
		echo "agent.toml already exists, skipping."; \
	fi
	@if [ ! -f policy.toml ]; then \
		echo '# Development policy - permissive' > policy.toml; \
		echo 'default_deny = false' >> policy.toml; \
		echo '' >> policy.toml; \
		echo '[tools.bash]' >> policy.toml; \
		echo 'enabled = true' >> policy.toml; \
		echo "Created: policy.toml"; \
	else \
		echo "policy.toml already exists, skipping."; \
	fi

# =============================================================================
# Clean Targets
# =============================================================================

.PHONY: clean
clean: ## Remove build artifacts
	@echo "Cleaning..."
	@rm -rf $(BIN_DIR)
	@rm -f coverage.out coverage.html
	@echo "Clean complete."

.PHONY: clean-all
clean-all: clean ## Remove all generated files including caches
	@$(GO) clean -cache -testcache
	@echo "Caches cleared."

# =============================================================================
# Release Targets
# =============================================================================

.PHONY: release
release: clean test build-all ## Create release artifacts
	@echo "Creating release $(VERSION)..."
	@mkdir -p dist
	@for f in $(BIN_DIR)/*; do \
		name=$$(basename $$f); \
		tar -czf dist/$$name-$(VERSION).tar.gz -C $(BIN_DIR) $$name; \
	done
	@echo "Release artifacts in dist/"

.PHONY: checksums
checksums: ## Generate checksums for release
	@cd dist && sha256sum * > SHA256SUMS

# =============================================================================
# Validation
# =============================================================================

.PHONY: validate-examples
validate-examples: build ## Validate all example Agentfiles
	@echo "Validating examples..."
	@for f in examples/*/*.agent examples/*.agent 2>/dev/null; do \
		if [ -f "$$f" ]; then \
			echo "Validating: $$f"; \
			$(BIN_DIR)/$(BINARY_NAME) validate "$$f" || exit 1; \
		fi \
	done
	@echo "All examples valid."

# =============================================================================
# Help
# =============================================================================

.PHONY: help
help: ## Show this help
	@echo "Headless Agent - Build & Development Commands"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Examples:"
	@echo "  make build          # Build the agent"
	@echo "  make install        # Install using go install"
	@echo "  make test           # Run tests"
	@echo "  make docker-build   # Build Docker image"
	@echo ""
	@echo "Remote install:"
	@echo "  go install github.com/vinayprograms/agent/cmd/agent@latest"
	@echo "  go install github.com/vinayprograms/agent/cmd/replay@latest"
