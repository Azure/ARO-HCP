SHELL = /bin/bash

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

run:
	go run ./...  --config-file ../../observability/observability.yaml $(EXTRA_FLAGS)
.PHONY: run

run-hcp:
	go run ./...  --config-file ../../observability/observability-hcp.yaml $(EXTRA_FLAGS)
.PHONY: run-hcp

run-hcp-sl:
	go run ./...  --config-file ../../observability/observability-hcp-sl.yaml $(EXTRA_FLAGS)
.PHONY: run-hcp-sl

run-msft:
	go run ./...  --config-file ../../observability/observability-msft.yaml $(EXTRA_FLAGS)
.PHONY: run-msft

run-recording-rules:
	go run ./...  --config-file ../../observability/observability-recording-rules.yaml
.PHONY: run-recording-rules
