SHELL = /bin/bash

# Define the binary name
BINARY = secret-sync

# Define the source files
SOURCES = $(shell find . -name '*.go' -o -name '*.json')

# Build the binary
$(BINARY): $(SOURCES) $(MAKEFILE_LIST) go.mod go.sum
	go build -ldflags="-s -w" -o $(BINARY) .

# Clean the build artifacts
clean:
	rm -f $(BINARY)

# Validates the correctness of encrypted secrets (requires az login to dev tenant)
test-decrypt:
	go test -tags decrypttest -v -timeout 5m .

.PHONY: clean test-decrypt
