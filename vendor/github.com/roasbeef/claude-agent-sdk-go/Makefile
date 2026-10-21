# claude-agent-sdk-go Makefile

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOVET=$(GOCMD) vet

# Build tags
INTEGRATION_TAGS=integration

# Linter
GOLANGCI_LINT_VERSION=v2.1.6
GOLANGCI_LINT=$(shell which golangci-lint 2>/dev/null)

# Coverage
COVERAGE_DIR=coverage
COVERAGE_FILE=$(COVERAGE_DIR)/coverage.out
COVERAGE_HTML=$(COVERAGE_DIR)/coverage.html

# Packages and test filtering (supports both uppercase and lowercase).
PKG ?= $(pkg)
ifeq ($(PKG),)
    PKG = ./...
endif

case ?=
TEST_FLAGS =
ifdef case
    TEST_FLAGS = -run=$(case)
endif

.PHONY: all build test test-race test-integration lint lint-fix fmt vet \
        clean deps tidy coverage coverage-html help install-linter check \
        unit unit-race

# Default target
all: fmt lint test build

# Build the library (just verifies it compiles)
build:
	@echo "Building..."
	$(GOBUILD) $(PKG)

# Run unit tests (no integration tests)
test:
	@echo "Running unit tests..."
	$(GOTEST) -v -count=1 $(PKG)

# Run tests with race detector
test-race:
	@echo "Running tests with race detector..."
	$(GOTEST) -v -race -count=1 $(PKG)

# Run unit tests with optional pkg and case targeting.
# Usage: make unit pkg=./mcp case=TestToolCall
unit:
	@echo "Running unit tests..."
	$(GOTEST) -v -count=1 $(TEST_FLAGS) $(PKG)

# Run unit tests with race detector and optional pkg and case targeting.
# Usage: make unit-race pkg=. case=TestSubprocess
unit-race:
	@echo "Running unit tests with race detector..."
	$(GOTEST) -v -race -count=1 $(TEST_FLAGS) $(PKG)

# Run integration tests (requires CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY)
test-integration:
	@echo "Running integration tests..."
	@if [ -z "$$CLAUDE_CODE_OAUTH_TOKEN" ] && [ -z "$$ANTHROPIC_API_KEY" ]; then \
		echo "Warning: No API token set. Integration tests will be skipped."; \
	fi
	$(GOTEST) -v -tags=$(INTEGRATION_TAGS) -count=1 $(PKG)

# Run all tests including integration
test-all: test-race test-integration

# Install golangci-lint if not present
install-linter:
	@if [ -z "$(GOLANGCI_LINT)" ]; then \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	else \
		echo "golangci-lint already installed at $(GOLANGCI_LINT)"; \
	fi

# Run linter
lint: install-linter
	@echo "Running linter..."
	golangci-lint run $(PKG)

# Run linter and fix issues
lint-fix: install-linter
	@echo "Running linter with fixes..."
	golangci-lint run --fix $(PKG)

# Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

# Run go vet
vet:
	@echo "Running go vet..."
	$(GOVET) $(PKG)

# Run all checks (fmt, vet, lint, test)
check: fmt vet lint test

# Generate test coverage
coverage:
	@echo "Generating coverage..."
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -coverprofile=$(COVERAGE_FILE) -covermode=atomic $(PKG)
	$(GOCMD) tool cover -func=$(COVERAGE_FILE)

# Generate HTML coverage report
coverage-html: coverage
	@echo "Generating HTML coverage report..."
	$(GOCMD) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Coverage report: $(COVERAGE_HTML)"

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	$(GOMOD) tidy

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(COVERAGE_DIR)
	$(GOCMD) clean -cache -testcache

# Help
help:
	@echo "claude-agent-sdk-go Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all              Format, lint, test, and build (default)"
	@echo "  build            Build the library"
	@echo "  test             Run unit tests"
	@echo "  test-race        Run unit tests with race detector"
	@echo "  unit             Run unit tests with PKG/TEST targeting"
	@echo "  unit-race        Run unit tests with race detector and PKG/TEST targeting"
	@echo "  test-integration Run integration tests (requires API token)"
	@echo "  test-all         Run all tests including integration"
	@echo "  lint             Run golangci-lint"
	@echo "  lint-fix         Run golangci-lint with auto-fix"
	@echo "  fmt              Format code with gofmt"
	@echo "  vet              Run go vet"
	@echo "  check            Run all checks (fmt, vet, lint, test)"
	@echo "  coverage         Generate test coverage report"
	@echo "  coverage-html    Generate HTML coverage report"
	@echo "  tidy             Tidy go.mod"
	@echo "  deps             Download dependencies"
	@echo "  clean            Clean build artifacts"
	@echo "  install-linter   Install golangci-lint"
	@echo "  help             Show this help"
	@echo ""
	@echo "Variables:"
	@echo "  pkg=./...        Package(s) to test (default: ./...)"
	@echo "  case=TestName    Test name filter (passed to -run)"
	@echo ""
	@echo "Examples:"
	@echo "  make unit pkg=. case=TestSubprocessTransport"
	@echo "  make unit-race case=TestStderr"
