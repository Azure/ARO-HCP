THIS_DIR := $(dir $(lastword $(MAKEFILE_LIST)))

SHELL = /bin/bash

# Arch-suffix avoids cross-platform conflicts when repo is bind-mounted in Linux containers
TEMPLATIZE_BINARY = templatize-$(shell uname -m)

# Define the source files
SOURCES = $(shell find $(THIS_DIR) -name '*.go' -o -name '*.json')

# Build the binary
TEMPLATIZE = $(THIS_DIR)$(TEMPLATIZE_BINARY)
$(TEMPLATIZE): $(SOURCES) $(MAKEFILE_LIST) $(THIS_DIR)/go.mod $(THIS_DIR)/go.sum
	cd $(THIS_DIR) && go build -mod=readonly -o $(TEMPLATIZE_BINARY) .

templatize: $(TEMPLATIZE)
