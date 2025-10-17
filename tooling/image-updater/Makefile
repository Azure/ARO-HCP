.PHONY: build update clean test test-verbose test-coverage help

BINARY_NAME := image-updater
CONFIG_FILE := config.yaml
COMPONENTS ?=
EXCLUDE_COMPONENTS ?=
COMPONENT_FLAGS := $(if $(COMPONENTS),--components $(COMPONENTS))
EXCLUDE_FLAGS := $(if $(EXCLUDE_COMPONENTS),--exclude-components $(EXCLUDE_COMPONENTS))

.DEFAULT_GOAL := help

build:
	@go build -o $(BINARY_NAME) .

update: build
	@./$(BINARY_NAME) update --config $(CONFIG_FILE) $(COMPONENT_FLAGS) $(EXCLUDE_FLAGS) && make -C ../../ yamlfmt > /dev/null 2>&1

test:
	@go test ./...

test-verbose:
	@go test ./... -v

test-coverage:
	@go test ./... -cover

clean:
	rm -f $(BINARY_NAME)

help:
	@echo "build update test test-verbose test-coverage clean"