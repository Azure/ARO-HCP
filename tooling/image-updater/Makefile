.PHONY: build update promote-stage promote-prod clean test test-verbose test-coverage help

BINARY_NAME := image-updater
CONFIG_FILE := config.yaml
COMPONENTS ?=
EXCLUDE_COMPONENTS ?=
VERBOSITY ?= 0
OUTPUT_FILE ?=
OUTPUT_FORMAT ?=
VERBOSITY_FLAGS := $(if $(VERBOSITY),--verbosity $(VERBOSITY))
COMPONENT_FLAGS := $(if $(COMPONENTS),--components $(COMPONENTS))
EXCLUDE_FLAGS := $(if $(EXCLUDE_COMPONENTS),--exclude-components $(EXCLUDE_COMPONENTS))
OUTPUT_FLAGS := $(if $(OUTPUT_FILE),--output-file $(OUTPUT_FILE)) $(if $(OUTPUT_FORMAT),--output-format $(OUTPUT_FORMAT))

.DEFAULT_GOAL := help

build:
	@go build -o $(BINARY_NAME) .

update: build
	@./$(BINARY_NAME) update --config $(CONFIG_FILE) $(COMPONENT_FLAGS) $(EXCLUDE_FLAGS) $(VERBOSITY_FLAGS) $(OUTPUT_FLAGS) && make -C ../../ yamlfmt > /dev/null 2>&1

promote-stage: build
	@./$(BINARY_NAME) update --config $(CONFIG_FILE) --env stg $(COMPONENT_FLAGS) $(EXCLUDE_FLAGS) $(VERBOSITY_FLAGS) $(OUTPUT_FLAGS) && make -C ../../ yamlfmt > /dev/null 2>&1

promote-prod: build
	@./$(BINARY_NAME) update --config $(CONFIG_FILE) --env prod $(COMPONENT_FLAGS) $(EXCLUDE_FLAGS) $(VERBOSITY_FLAGS) $(OUTPUT_FLAGS) && make -C ../../ yamlfmt > /dev/null 2>&1

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
	@echo "  update             - Build and run the updater (updates dev,int)"
	@echo "  promote-stage      - Promote images from int to stage"
	@echo "  promote-prod       - Promote images from stage to prod"
	@echo "  test               - Run tests"
	@echo "  test-verbose       - Run tests with verbose output"
	@echo "  test-coverage      - Run tests with coverage"
	@echo "  clean              - Remove built binary"
	@echo ""
	@echo "Variables:"
	@echo "  COMPONENTS         - Comma-separated list of components to update"
	@echo "  EXCLUDE_COMPONENTS - Comma-separated list of components to exclude"
	@echo "  VERBOSITY          - Set verbosity level (default: 0, 0-1: summary, 2+: debug)"
	@echo "  OUTPUT_FILE        - Write results to file instead of stdout"
	@echo "  OUTPUT_FORMAT      - Output format: table, markdown, json (default: table)"
	@echo ""
	@echo "Examples:"
	@echo "  make update VERBOSITY=2"
	@echo "  make update COMPONENTS=frontend,backend"
	@echo "  make update EXCLUDE_COMPONENTS=maestro VERBOSITY=1"
	@echo "  make update OUTPUT_FILE=results.md OUTPUT_FORMAT=markdown"
	@echo "  make promote-stage COMPONENTS=maestro OUTPUT_FILE=/tmp/promo.json OUTPUT_FORMAT=json"
	@echo "  make promote-prod VERBOSITY=2"