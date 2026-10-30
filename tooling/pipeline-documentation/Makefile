SHELL = /bin/bash

# Define the binary name
BINARY = pipeline-documentation

# Define the source files
SOURCES = $(shell find . -name '*.go')

# Build the binary
$(BINARY): $(SOURCES) $(MAKEFILE_LIST) force-go-build
	go build -o $(BINARY) .

# Clean the build artifacts
clean:
	rm -f $(BINARY)

.PHONY: clean

# Always consult Go's build cache, including for dependency-only changes.
.PHONY: force-go-build
force-go-build:
