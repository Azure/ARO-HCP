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

ALERT_CONFIGS = alerts-sl-services alerts-sl-hcps alerts-sre-hcps alerts-sre-services alerts-rp-services alerts-msft-services alerts-kusto-services alerts-kusto-hcps
RECORDING_CONFIGS = recording-rules-services recording-rules-hcps
CONFIGS = $(ALERT_CONFIGS) $(RECORDING_CONFIGS)

alerts: $(BINARY)
	@set -e; for cfg in $(ALERT_CONFIGS); do \
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

recording-rules: $(BINARY)
	@set -e; for cfg in $(RECORDING_CONFIGS); do \
		./$(BINARY) --skip-tests --config-file ../../observability/$${cfg}.yaml $(EXTRA_FLAGS); \
	done
	$(MAKE) fmt-devinfra
.PHONY: recording-rules

CORRELATION_MAP_FILES = $(addprefix ../../observability/,$(addsuffix .yaml,$(ALERT_CONFIGS)))

correlation-map: $(BINARY)
	./$(BINARY) --correlation-map $(CORRELATION_MAP_FILES) > $(OUTPUT)
.PHONY: correlation-map
