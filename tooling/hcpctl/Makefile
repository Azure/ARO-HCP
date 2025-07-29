SHELL = /bin/bash

# Binary name
BINARY = hcpctl

# Version information
CURRENT_COMMIT ?= $(shell git rev-parse --short=7 HEAD)
VERSION ?= $(shell git describe --tags 2>/dev/null || echo "v0.0.0-${CURRENT_COMMIT}")
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

# Build flags
LDFLAGS = -ldflags "\
	-X github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/version.commit=${CURRENT_COMMIT} \
	-X github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/version.buildDate=${BUILD_DATE}"

# Source files
SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

# Default target
.DEFAULT_GOAL := build

# Build the binary
build: $(BINARY)
.PHONY: build

$(BINARY): $(SOURCES) $(MAKEFILE_LIST)
	go build $(LDFLAGS) -o $(BINARY) .

# Run tests
test:
	go test -v -timeout 300s -cover ./...
.PHONY: test


# Run Go-based end-to-end tests
test-e2e: build
	@echo "Running Go-based e2e tests in parallel..."
	E2E_MC_CLUSTER=$(shell cat ../../config/rendered/dev/dev/westus3.yaml | yq .mgmt.aks.name) go test -v -tags=E2Etests -timeout=10m -parallel 16 ./internal/e2e/
.PHONY: test-e2e

# Build for Windows x86_64
build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-amd64.exe .
.PHONY: build-windows-amd64

# Build for Windows ARM64
build-windows-arm64:
	GOOS=windows GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-arm64.exe .
.PHONY: build-windows-arm64

# Build both Windows binaries
build-windows: build-windows-amd64 build-windows-arm64
.PHONY: build-windows

# Clean build artifacts
clean:
	rm -f $(BINARY)
	rm -f $(BINARY)-*.exe
	rm -f coverage.out coverage.html
.PHONY: clean

# Help target
help:
	@echo "Available targets:"
	@echo "  build                - Build the hcpctl binary (default)"
	@echo "  build-windows        - Build both Windows binaries (amd64 and arm64)"
	@echo "  build-windows-amd64  - Build Windows x86_64 binary"
	@echo "  build-windows-arm64  - Build Windows ARM64 binary"
	@echo "  test                 - Run unit tests"
	@echo "  test-e2e             - Run Go-based end-to-end tests (set E2E_MC_CLUSTER=<name> to use different cluster)"
	@echo "  clean                - Remove build artifacts"
	@echo "  help                 - Show this help message"
.PHONY: help
