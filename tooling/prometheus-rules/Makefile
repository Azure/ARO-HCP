SHELL = /bin/bash

PROMTOOL ?= promtool

# Define the binary name
BINARY = prometheus-rules

# Define the source files
SOURCES = $(shell find . -name '*.go')

# Build the binary
$(BINARY): $(SOURCES) $(MAKEFILE_LIST)
	go build -o $(BINARY) .

# Clean the build artifacts
clean:
	rm -f $(BINARY)

.PHONY: clean

fmt-devinfra:
	$(MAKE) -C ../../dev-infrastructure fmt
.PHONY: fmt-devinfra

run:
	go run ./...  --config-file ../../observability/observability.yaml --promtool-path $(PROMTOOL) $(EXTRA_FLAGS)
	$(MAKE) fmt-devinfra
.PHONY: run

run-hcp:
	go run ./...  --config-file ../../observability/observability-hcp.yaml --promtool-path $(PROMTOOL) $(EXTRA_FLAGS)
	$(MAKE) fmt-devinfra
.PHONY: run-hcp

run-rp:
	go run ./...  --config-file ../../observability/observability-rp.yaml --promtool-path $(PROMTOOL) $(EXTRA_FLAGS)
	$(MAKE) fmt-devinfra
.PHONY: run-rp

run-msft:
	go run ./...  --config-file ../../observability/observability-msft.yaml --promtool-path $(PROMTOOL) $(EXTRA_FLAGS)
	$(MAKE) fmt-devinfra
.PHONY: run-msft

run-recording-rules:
	go run ./...  --config-file ../../observability/observability-recording-rules.yaml --promtool-path $(PROMTOOL) $(EXTRA_FLAGS)
	$(MAKE) fmt-devinfra
.PHONY: run-recording-rules

run-hcp-recording-rules:
	go run ./...  --config-file ../../observability/observability-hcp-recording-rules.yaml --promtool-path $(PROMTOOL) $(EXTRA_FLAGS)
	$(MAKE) fmt-devinfra
.PHONY: run-hcp-recording-rules
