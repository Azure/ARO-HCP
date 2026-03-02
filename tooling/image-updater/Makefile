.PHONY: build update clean test test-verbose test-coverage help

BINARY_NAME := image-updater
CONFIG_FILE := config.yaml
COMPONENTS ?=
GROUPS ?=
EXCLUDE_COMPONENTS ?=
VERBOSITY ?= 0
OUTPUT_FILE ?=
OUTPUT_FORMAT ?= markdown
VERBOSITY_FLAGS := $(if $(VERBOSITY),--verbosity $(VERBOSITY))
COMPONENT_FLAGS := $(if $(COMPONENTS),--components $(COMPONENTS))
GROUP_FLAGS := $(if $(GROUPS),--groups $(GROUPS))
EXCLUDE_FLAGS := $(if $(EXCLUDE_COMPONENTS),--exclude-components $(EXCLUDE_COMPONENTS))
OUTPUT_FLAGS := $(if $(OUTPUT_FILE),--output-file $(OUTPUT_FILE)) $(if $(OUTPUT_FORMAT),--output-format $(OUTPUT_FORMAT))

.DEFAULT_GOAL := help

build:
	@go build -o $(BINARY_NAME) .

update: build
	@./$(BINARY_NAME) update --config $(CONFIG_FILE) $(COMPONENT_FLAGS) $(GROUP_FLAGS) $(EXCLUDE_FLAGS) $(VERBOSITY_FLAGS) $(OUTPUT_FLAGS)

test:
	@go test ./...

test-verbose:
	@go test ./... -v

test-coverage:
	@go test ./... -cover

clean:
	rm -f $(BINARY_NAME)

help:
	@echo "Available targets:"
	@echo "  build              - Build the image-updater binary"
	@echo "  update             - Build and run the updater"
	@echo "  test               - Run tests"
	@echo "  test-verbose       - Run tests with verbose output"
	@echo "  test-coverage      - Run tests with coverage"
	@echo "  clean              - Remove built binary"
	@echo ""
	@echo "Variables:"
	@echo "  COMPONENTS         - Comma-separated list of components to update"
	@echo "  GROUPS             - Comma-separated list of groups to update (e.g., hypershift-stack,velero)"
	@echo "  EXCLUDE_COMPONENTS - Comma-separated list of components to exclude"
	@echo "  VERBOSITY          - Set verbosity level (default: 0, 0-1: summary, 2+: debug)"
	@echo "  OUTPUT_FILE        - Write results to file instead of stdout"
	@echo "  OUTPUT_FORMAT      - Output format: table, markdown, json (default: table)"
	@echo ""
	@echo "Examples:"
	@echo "  make update VERBOSITY=2"
	@echo "  make update COMPONENTS=frontend,backend"
	@echo "  make update GROUPS=hypershift-stack,velero"
	@echo "  make update GROUPS=hypershift-stack EXCLUDE_COMPONENTS=maestro-agent-sidecar"
	@echo "  make update EXCLUDE_COMPONENTS=maestro VERBOSITY=1"
	@echo "  make update OUTPUT_FILE=results.md OUTPUT_FORMAT=markdown"
