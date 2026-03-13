-include ../../setup-templatize-env.mk
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)

SHELL = /bin/bash

# Binary name
BINARY = aro-hcp-exporter

# Source files
SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

# Default target
.DEFAULT_GOAL := build

ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_EXPORTER_IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../../generate-tag.sh)
ARO_HCP_EXPORTER_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${ARO_HCP_EXPORTER_IMAGE_REPOSITORY} ../../generate-repo.sh)
ARO_HCP_EXPORTER_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(ARO_HCP_EXPORTER_GENERATED_IMAGE_REPOSITORY):$(ARO_HCP_EXPORTER_IMAGE_TAG)

# Build the binary
build: $(BINARY)
.PHONY: build

$(BINARY): $(SOURCES) $(MAKEFILE_LIST)
	go build -o $(BINARY) .

# Run tests
test:
	go test -v -timeout 300s -cover ./...
.PHONY: test

# Run the exporter
run: build
	./$(BINARY) serve
.PHONY: run

# Clean build artifacts
clean:
	rm -f $(BINARY)
	rm -f coverage.out coverage.html
.PHONY: clean

image: Dockerfile Makefile
	$(CONTAINER_ENGINE) build . --file Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--tag ${ARO_HCP_EXPORTER_TAGGED_IMAGE}
.PHONY: image

build-and-push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	$(CONTAINER_ENGINE) push ${ARO_HCP_EXPORTER_TAGGED_IMAGE}
.PHONY: build-and-push

print-image-vars:
	@echo "ARO_HCP_IMAGE_ACR=${ARO_HCP_IMAGE_ACR}" > $(OUTPUT_FILE)
	@echo "ARO_HCP_EXPORTER_TAGGED_IMAGE=${ARO_HCP_EXPORTER_TAGGED_IMAGE}" >> $(OUTPUT_FILE)
.PHONY: print-image-vars

