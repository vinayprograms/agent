# Headless Agent Makefile

# Variables
BINARY_NAME := agent
REPLAY_BINARY := agent-replay
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Directories
SRC_DIR := src
BIN_DIR := bin
INSTALL_DIR := $(HOME)/.local/bin

# Build flags
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)"
CGO_ENABLED := 1

# SQLite headers for sqlite-vec CGO bindings
# Linux: /usr/include, macOS: typically handled by Homebrew or Xcode
CGO_CFLAGS ?= $(shell pkg-config --cflags sqlite3 2>/dev/null || echo "-I/usr/include")

# Go commands
GO := go
GOTEST := $(GO) test
GOBUILD := CGO_ENABLED=$(CGO_ENABLED) CGO_CFLAGS="$(CGO_CFLAGS)" $(GO) build $(LDFLAGS)

# Default target
.DEFAULT_GOAL := build

# =============================================================================
# Build Targets
# =============================================================================

.PHONY: build
build: ## Build agent and agent-replay binaries
	@echo "Building $(BINARY_NAME) and $(REPLAY_BINARY) $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	@cd $(SRC_DIR) && $(GOBUILD) -o ../$(BIN_DIR)/$(BINARY_NAME) ./cmd/agent
	@cd $(SRC_DIR) && $(GOBUILD) -o ../$(BIN_DIR)/$(REPLAY_BINARY) ./cmd/replay
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME), $(BIN_DIR)/$(REPLAY_BINARY)"

.PHONY: build-agent
build-agent: ## Build only the agent binary
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	@cd $(SRC_DIR) && $(GOBUILD) -o ../$(BIN_DIR)/$(BINARY_NAME) ./cmd/agent
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)"

.PHONY: build-replay
build-replay: ## Build only the replay binary
	@echo "Building $(REPLAY_BINARY) $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	@cd $(SRC_DIR) && $(GOBUILD) -o ../$(BIN_DIR)/$(REPLAY_BINARY) ./cmd/replay
	@echo "Built: $(BIN_DIR)/$(REPLAY_BINARY)"

.PHONY: build-static
build-static: ## Build fully static binaries (for containers)
	@echo "Building static binaries $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	@cd $(SRC_DIR) && CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(BINARY_NAME)-static ./cmd/agent
	@cd $(SRC_DIR) && CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(REPLAY_BINARY)-static ./cmd/replay
	@echo "Built: $(BIN_DIR)/$(BINARY_NAME)-static, $(BIN_DIR)/$(REPLAY_BINARY)-static"

.PHONY: build-all
build-all: ## Build for all platforms
	@echo "Building for all platforms..."
	@mkdir -p $(BIN_DIR)
	@cd $(SRC_DIR) && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/agent
	@cd $(SRC_DIR) && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(REPLAY_BINARY)-linux-amd64 ./cmd/replay
	@cd $(SRC_DIR) && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/agent
	@cd $(SRC_DIR) && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(REPLAY_BINARY)-linux-arm64 ./cmd/replay
	@cd $(SRC_DIR) && GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/agent
	@cd $(SRC_DIR) && GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(REPLAY_BINARY)-darwin-amd64 ./cmd/replay
	@cd $(SRC_DIR) && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/agent
	@cd $(SRC_DIR) && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(REPLAY_BINARY)-darwin-arm64 ./cmd/replay
	@cd $(SRC_DIR) && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/agent
	@cd $(SRC_DIR) && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o ../$(BIN_DIR)/$(REPLAY_BINARY)-windows-amd64.exe ./cmd/replay
	@echo "Built binaries in $(BIN_DIR)/"

# =============================================================================
# Install Targets
# =============================================================================

.PHONY: install
install: build ## Install both binaries to ~/.local/bin
	@echo "Installing to $(INSTALL_DIR)..."
	@mkdir -p $(INSTALL_DIR)
	@cp $(BIN_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	@cp $(BIN_DIR)/$(REPLAY_BINARY) $(INSTALL_DIR)/$(REPLAY_BINARY)
	@chmod 755 $(INSTALL_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(REPLAY_BINARY)
	@echo "Installed: $(INSTALL_DIR)/$(BINARY_NAME), $(INSTALL_DIR)/$(REPLAY_BINARY)"
	@echo ""
	@echo "Make sure $(INSTALL_DIR) is in your PATH:"
	@echo "  export PATH=\"\$$HOME/.local/bin:\$$PATH\""

.PHONY: install-system
install-system: build ## Install to /usr/local/bin (requires sudo)
	@echo "Installing to /usr/local/bin..."
	@sudo cp $(BIN_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	@sudo cp $(BIN_DIR)/$(REPLAY_BINARY) /usr/local/bin/$(REPLAY_BINARY)
	@sudo chmod 755 /usr/local/bin/$(BINARY_NAME) /usr/local/bin/$(REPLAY_BINARY)
	@echo "Installed: /usr/local/bin/$(BINARY_NAME), /usr/local/bin/$(REPLAY_BINARY)"

.PHONY: uninstall
uninstall: ## Remove from ~/.local/bin
	@echo "Removing $(INSTALL_DIR)/$(BINARY_NAME) and $(REPLAY_BINARY)..."
	@rm -f $(INSTALL_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(REPLAY_BINARY)
	@echo "Uninstalled."

# =============================================================================
# Test Targets
# =============================================================================

.PHONY: test
test: ## Run all tests
	@echo "Running tests..."
	@cd $(SRC_DIR) && $(GOTEST) ./...

.PHONY: test-verbose
test-verbose: ## Run tests with verbose output
	@cd $(SRC_DIR) && $(GOTEST) -v ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage
	@cd $(SRC_DIR) && $(GOTEST) -coverprofile=coverage.out ./...
	@cd $(SRC_DIR) && $(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: $(SRC_DIR)/coverage.html"

.PHONY: test-race
test-race: ## Run tests with race detector
	@cd $(SRC_DIR) && $(GOTEST) -race ./...

# =============================================================================
# Development Targets
# =============================================================================

.PHONY: fmt
fmt: ## Format code
	@cd $(SRC_DIR) && $(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	@cd $(SRC_DIR) && $(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@cd $(SRC_DIR) && golangci-lint run

.PHONY: deps
deps: ## Download dependencies
	@cd $(SRC_DIR) && $(GO) mod download

.PHONY: deps-update
deps-update: ## Update dependencies
	@cd $(SRC_DIR) && $(GO) get -u ./...
	@cd $(SRC_DIR) && $(GO) mod tidy

.PHONY: tidy
tidy: ## Tidy go.mod
	@cd $(SRC_DIR) && $(GO) mod tidy

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
		echo '' >> agent.toml; \
		echo '[session]' >> agent.toml; \
		echo 'store = "file"' >> agent.toml; \
		echo 'path = "./sessions"' >> agent.toml; \
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
	@rm -f $(SRC_DIR)/coverage.out $(SRC_DIR)/coverage.html
	@echo "Clean complete."

.PHONY: clean-all
clean-all: clean ## Remove all generated files including caches
	@cd $(SRC_DIR) && $(GO) clean -cache -testcache
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
	@echo "  make install        # Install to ~/.local/bin"
	@echo "  make test           # Run tests"
	@echo "  make setup          # Run interactive setup wizard"
	@echo "  make docker-build   # Build Docker image"
