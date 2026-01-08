.PHONY: build update clean test test-verbose test-coverage help

BINARY_NAME := image-updater
CONFIG_FILE := config.yaml
COMPONENTS ?=
EXCLUDE_COMPONENTS ?=
VERBOSITY ?= 0
VERBOSITY_FLAGS := $(if $(VERBOSITY),--verbosity $(VERBOSITY))
COMPONENT_FLAGS := $(if $(COMPONENTS),--components $(COMPONENTS))
EXCLUDE_FLAGS := $(if $(EXCLUDE_COMPONENTS),--exclude-components $(EXCLUDE_COMPONENTS))

.DEFAULT_GOAL := help

build:
	@go build -o $(BINARY_NAME) .

update: build
	@./$(BINARY_NAME) update --config $(CONFIG_FILE) $(COMPONENT_FLAGS) $(EXCLUDE_FLAGS) $(VERBOSITY_FLAGS) && make -C ../../ yamlfmt > /dev/null 2>&1

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
	@echo "  EXCLUDE_COMPONENTS - Comma-separated list of components to exclude"
	@echo "  VERBOSITY          - Set verbosity level (default: 0, 0-1: summary, 2+: debug)"
	@echo ""
	@echo "Examples:"
	@echo "  make update VERBOSITY=2"
	@echo "  make update COMPONENTS=frontend,backend"
	@echo "  make update EXCLUDE_COMPONENTS=maestro VERBOSITY=1"