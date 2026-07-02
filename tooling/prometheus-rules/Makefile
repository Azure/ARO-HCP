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

CONFIGS = alerts-sl-services alerts-sl-hcps alerts-sre-hcps alerts-sre-services alerts-rp-services alerts-msft-services recording-rules-services recording-rules-hcps

alerts: $(BINARY)
	@set -e; for cfg in $(CONFIGS); do \
		./$(BINARY) --skip-tests --config-file ../../observability/$${cfg}.yaml $(EXTRA_FLAGS); \
	done
	$(MAKE) fmt-devinfra
.PHONY: alerts

alerts-and-test: $(BINARY)
	@set -e; for cfg in $(CONFIGS); do \
		./$(BINARY) --config-file ../../observability/$${cfg}.yaml $(EXTRA_FLAGS); \
	done
	$(MAKE) fmt-devinfra
.PHONY: alerts-and-test

run: alerts
.PHONY: run

RUN_TARGETS = $(addprefix run-,$(CONFIGS))

$(RUN_TARGETS): run-%: $(BINARY)
	./$(BINARY) --config-file ../../observability/$*.yaml $(EXTRA_FLAGS)

.PHONY: $(RUN_TARGETS)
