-include ../../setup-env.mk
-include ../../setup-templatize-env.mk
-include ../../.bingo/Variables.mk

SHELL = /bin/bash

# Binary name
BINARY = aro-hcp-exporter

# Source files
SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

# Default target
.DEFAULT_GOAL := build

ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_EXPORTER_IMAGE_TAG ?= $(or $(IMAGE_TAG),$(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../../generate-tag.sh))
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
	$(CONTAINER_ENGINE) build ../../ --file Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--tag ${ARO_HCP_EXPORTER_TAGGED_IMAGE}
.PHONY: image

build-and-push: image $(ORAS) $(YQ)
	$(acr-login)
	@LOCAL_DIGEST=$$($(CONTAINER_ENGINE) image inspect --format '{{.Id}}' ${ARO_HCP_EXPORTER_TAGGED_IMAGE} | sed 's/sha256://'); \
	REMOTE_DIGEST=$$($(ORAS) manifest fetch ${ARO_HCP_EXPORTER_TAGGED_IMAGE} 2>/dev/null | $(YQ) .config.digest | sed 's/sha256://'); \
	if [ "$$LOCAL_DIGEST" = "$$REMOTE_DIGEST" ]; then \
		echo "Image ${ARO_HCP_EXPORTER_TAGGED_IMAGE} already up to date in registry, skipping push"; \
	else \
	    echo "pushing image to registry"; \
		$(CONTAINER_ENGINE) push ${ARO_HCP_EXPORTER_TAGGED_IMAGE}; \
	fi
.PHONY: build-and-push

MAKE_RUN_TS := $(shell date +%s)
OVERRIDE_CONFIG_FILE ?= /tmp/aro-hcp-exporter-override-config-$(MAKE_RUN_TS).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
	$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
		--username 00000000-0000-0000-0000-000000000000 \
		--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${ARO_HCP_EXPORTER_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.customExporter.image.registry = \"$(ARO_HCP_IMAGE_REGISTRY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.customExporter.image.repository = \"$(ARO_HCP_EXPORTER_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.customExporter.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

record-latest-override: $(YQ)
	@../../hack/record-latest-image-override.sh \
		"$(ARO_HCP_IMAGE_ACR)" "$(ARO_HCP_IMAGE_REGISTRY)" \
		"$(ARO_HCP_EXPORTER_GENERATED_IMAGE_REPOSITORY)" "$(DEPLOY_ENV)" \
		"customExporter" "$(OVERRIDE_CONFIG_FILE)" "$(YQ)"
.PHONY: record-latest-override
