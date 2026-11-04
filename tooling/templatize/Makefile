THIS_DIR := $(dir $(lastword $(MAKEFILE_LIST)))

SHELL = /bin/bash

# Arch-suffix avoids cross-platform conflicts when repo is bind-mounted in Linux containers
TEMPLATIZE_BINARY = templatize-$(shell uname -m)

# Define the source files
SOURCES = $(shell find $(THIS_DIR) -name '*.go' -o -name '*.json')

# Build the binary
TEMPLATIZE = $(THIS_DIR)$(TEMPLATIZE_BINARY)
$(TEMPLATIZE): $(SOURCES) $(MAKEFILE_LIST) $(THIS_DIR)/go.mod $(THIS_DIR)/go.sum force-go-build
	@# Preserve the timestamp when unchanged: generated environment Makefiles depend on this tool.
	set -e; cd $(THIS_DIR); \
		tmp=$$(mktemp ./$(TEMPLATIZE_BINARY).XXXXXX); \
		trap 'rm -f "$$tmp"' EXIT; \
		if test -f $(TEMPLATIZE_BINARY); then cp $(TEMPLATIZE_BINARY) "$$tmp"; fi; \
		go build -mod=vendor -o "$$tmp" .; \
		if ! cmp -s "$$tmp" $(TEMPLATIZE_BINARY); then mv "$$tmp" $(TEMPLATIZE_BINARY); fi

templatize: $(TEMPLATIZE)
.PHONY: templatize

# Always consult Go's build cache, including for dependency-only changes.
.PHONY: force-go-build
force-go-build:
