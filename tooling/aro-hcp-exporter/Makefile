SHELL = /bin/bash

# Binary name
BINARY = aro-hcp-exporter

# Source files
SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

# Default target
.DEFAULT_GOAL := build

# Build the binary
build: $(BINARY)
.PHONY: build

$(BINARY): $(SOURCES) $(MAKEFILE_LIST)
	go build -o $(BINARY) .

# Run tests
test:
	go test -v -timeout 300s -cover ./...
.PHONY: test

# Run the exporter
run: build
	./$(BINARY) serve
.PHONY: run

# Clean build artifacts
clean:
	rm -f $(BINARY)
	rm -f coverage.out coverage.html
.PHONY: clean

# Help target
help:
	@echo "Available targets:"
	@echo "  build  - Build the aro-hcp-exporter binary (default)"
	@echo "  test   - Run unit tests"
	@echo "  run    - Build and run the exporter"
	@echo "  clean  - Remove build artifacts"
	@echo "  help   - Show this help message"
.PHONY: help
