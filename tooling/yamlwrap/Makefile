SHELL = /bin/bash

# Define the binary name
BINARY = yamlwrap

# Define the source files
SOURCES = $(shell find . -name '*.go' -o -name '*.json')

# Build the binary
$(BINARY): $(SOURCES) $(MAKEFILE_LIST) go.mod go.sum
	go build -ldflags="-s -w" -o $(BINARY) .

# Clean the build artifacts
clean:
	rm -f $(BINARY)

.PHONY: clean
