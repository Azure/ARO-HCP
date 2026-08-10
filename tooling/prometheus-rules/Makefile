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

run: alerts recording-rules
.PHONY: run

alerts: run-sl-services run-sl-hcps run-sre-hcps run-sre-services run-rp-services run-rp-hcps run-msft-services run-kusto-services run-kusto-hcps
	$(MAKE) fmt-devinfra
.PHONY: alerts

recording-rules: run-recording-rules-services run-recording-rules-hcps
	$(MAKE) fmt-devinfra
.PHONY: recording-rules

run-sl-services:
	go run ./...  --config-file ../../observability/alerts-sl-services.yaml $(EXTRA_FLAGS)
.PHONY: run-sl-services

run-sl-hcps:
	go run ./...  --config-file ../../observability/alerts-sl-hcps.yaml $(EXTRA_FLAGS)
.PHONY: run-sl-hcps

run-sre-hcps:
	go run ./...  --config-file ../../observability/alerts-sre-hcps.yaml $(EXTRA_FLAGS)
.PHONY: run-sre-hcps

run-sre-services:
	go run ./...  --config-file ../../observability/alerts-sre-services.yaml $(EXTRA_FLAGS)
.PHONY: run-sre-services

run-rp-services:
	go run ./...  --config-file ../../observability/alerts-rp-services.yaml $(EXTRA_FLAGS)
.PHONY: run-rp-services

run-rp-hcps:
	go run ./...  --config-file ../../observability/alerts-rp-hcps.yaml $(EXTRA_FLAGS)
.PHONY: run-rp-hcps

run-msft-services:
	go run ./...  --config-file ../../observability/alerts-msft-services.yaml $(EXTRA_FLAGS)
.PHONY: run-msft-services

run-kusto-services:
	go run ./...  --config-file ../../observability/alerts-kusto-services.yaml $(EXTRA_FLAGS)
.PHONY: run-kusto-services

run-kusto-hcps:
	go run ./...  --config-file ../../observability/alerts-kusto-hcps.yaml $(EXTRA_FLAGS)
.PHONY: run-kusto-hcps

run-recording-rules-services:
	go run ./...  --config-file ../../observability/recording-rules-services.yaml $(EXTRA_FLAGS)
.PHONY: run-recording-rules-services

run-recording-rules-hcps:
	go run ./...  --config-file ../../observability/recording-rules-hcps.yaml $(EXTRA_FLAGS)
.PHONY: run-recording-rules-hcps

ALERT_CONFIGS = \
	../../observability/alerts-sl-services.yaml \
	../../observability/alerts-sl-hcps.yaml \
	../../observability/alerts-sre-hcps.yaml \
	../../observability/alerts-sre-services.yaml \
	../../observability/alerts-rp-services.yaml \
	../../observability/alerts-rp-hcps.yaml \
	../../observability/alerts-msft-services.yaml \
	../../observability/alerts-kusto-services.yaml \
	../../observability/alerts-kusto-hcps.yaml

correlation-map:
	go run ./... --correlation-map $(ALERT_CONFIGS) > $(OUTPUT)
.PHONY: correlation-map
