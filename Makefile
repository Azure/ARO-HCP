SHELL = /bin/bash

# This build tag is currently leveraged by tooling/image-sync
# https://github.com/containers/image?tab=readme-ov-file#building
GOTAGS?='containers_image_openpgp'
TOOLS_BIN_DIR := tooling/bin

all: test lint

# There is currently no convenient way to run tests against a whole Go workspace
# https://github.com/golang/go/issues/50745
test:
	go list -f '{{.Dir}}/...' -m | xargs go test -tags=$(GOTAGS) -cover

# There is currently no convenient way to run golangci-lint against a whole Go workspace
# https://github.com/golang/go/issues/50745
MODULES := $(shell go list -f '{{.Dir}}/...' -m | xargs)
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run -v --build-tags=$(GOTAGS) $(MODULES)

.PHONY: all clean lint test

GOLANGCI_LINT_BIN := golangci-lint
GOLANGCI_LINT_VER := $(shell cat .github/workflows/ci-go.yml | grep [[:space:]]version: | sed 's/.*version: //')
GOLANGCI_LINT := $(abspath $(TOOLS_BIN_DIR)/$(GOLANGCI_LINT_BIN)-$(GOLANGCI_LINT_VER))

$(GOLANGCI_LINT): # Setup a repo-local golangci-lint in $(GOLANGCI_LINT)
	$(shell curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(TOOLS_BIN_DIR) $(GOLANGCI_LINT_VER))
	$(shell mv $(TOOLS_BIN_DIR)/$(GOLANGCI_LINT_BIN) $(TOOLS_BIN_DIR)/$(GOLANGCI_LINT_BIN)-$(GOLANGCI_LINT_VER))
