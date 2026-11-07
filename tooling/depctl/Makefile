SHELL = /bin/bash

BINARY = depctl
CURRENT_COMMIT ?= $(shell git rev-parse --short=7 HEAD)
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

LDFLAGS = -ldflags "\
	-X github.com/Azure/ARO-HCP/tooling/depctl/internal/version.commit=${CURRENT_COMMIT} \
	-X github.com/Azure/ARO-HCP/tooling/depctl/internal/version.buildDate=${BUILD_DATE}"

SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

.DEFAULT_GOAL := build

build: $(BINARY)
.PHONY: build

$(BINARY): $(SOURCES) $(MAKEFILE_LIST) force-go-build
	go build $(LDFLAGS) -o $(BINARY) .

test:
	go test -timeout 600s ./...
.PHONY: test

# A complete local lifecycle against fixture repositories and fake services.
test-scenarios: $(BINARY)
	./$(BINARY) run --mode simulate --scenario lifecycle --work-dir "$$(mktemp -d)"
.PHONY: test-scenarios

clean:
	rm -f $(BINARY)
.PHONY: clean

.PHONY: force-go-build
force-go-build:
