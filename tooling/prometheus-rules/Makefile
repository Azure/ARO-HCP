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

fmt-devinfra:
	$(MAKE) -C ../../dev-infrastructure fmt
.PHONY: fmt-devinfra

run:
	go run ./...  --config-file ../../observability/observability.yaml $(EXTRA_FLAGS)
	$(MAKE) fmt-devinfra
.PHONY: run

run-hcp:
	go run ./...  --config-file ../../observability/observability-hcp.yaml $(EXTRA_FLAGS)
	$(MAKE) fmt-devinfra
.PHONY: run-hcp

run-msft:
	go run ./...  --config-file ../../observability/observability-msft.yaml $(EXTRA_FLAGS)
	$(MAKE) fmt-devinfra
.PHONY: run-msft

run-recording-rules:
	go run ./...  --config-file ../../observability/observability-recording-rules.yaml
	$(MAKE) fmt-devinfra
.PHONY: run-recording-rules

run-hcp-recording-rules:
	go run ./...  --config-file ../../observability/observability-hcp-recording-rules.yaml
	$(MAKE) fmt-devinfra
.PHONY: run-hcp-recording-rules
