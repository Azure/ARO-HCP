SHELL = /bin/bash

YAMLWRAP_DIR := $(dir $(lastword $(MAKEFILE_LIST)))

# Define the binary name
YAMLWRAP_BINARY = yamlwrap
YAMLWRAP = $(YAMLWRAP_DIR)yamlwrap

# Define the source files
YAMLWRAP_SOURCES = $(shell find $(YAMLWRAP_DIR) -name '*.go' -o -name '*.json')

# Build the binary
$(YAMLWRAP): $(YAMLWRAP_SOURCES) $(MAKEFILE_LIST) $(YAMLWRAP_DIR)/go.mod $(YAMLWRAP_DIR)/go.sum
	cd $(YAMLWRAP_DIR) && go build -ldflags="-s -w" -o $(YAMLWRAP_BINARY) .

# Clean the build artifacts
clean:
	rm -f $(YAMLWRAP)

.PHONY: clean
