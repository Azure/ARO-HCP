THIS_DIR := $(dir $(lastword $(MAKEFILE_LIST)))

SHELL = /bin/bash

# Define the binary name
BINARY = templatize

# Define the source files
SOURCES = $(shell find $(THIS_DIR) -name '*.go' -o -name '*.json')

# Build the binary
TEMPLATIZE = $(THIS_DIR)templatize
$(TEMPLATIZE): $(SOURCES) $(MAKEFILE_LIST) $(THIS_DIR)/go.mod $(THIS_DIR)/go.sum
	cd $(THIS_DIR) && go build -o templatize .
