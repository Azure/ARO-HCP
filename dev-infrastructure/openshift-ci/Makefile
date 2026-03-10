SHELL = /bin/bash

include versions.mk

IMAGE_NAME      ?= aro-hcp-ci-test
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)
PLATFORM ?= "linux/amd64"

VERSION_ARGS = BUILDER_IMAGE_TAG PROMTOOL_VERSION

.PHONY: verify build test

##@ Verification

verify: ## Check PROMTOOL_VERSION in versions.mk matches the Dockerfile ARG default
	@actual=$$(grep -oP 'ARG PROMTOOL_VERSION=\K.*' Dockerfile); \
	if [ "$$actual" != "$(PROMTOOL_VERSION)" ]; then \
		echo "ERROR: Dockerfile ARG PROMTOOL_VERSION=$$actual ≠ versions.mk $(PROMTOOL_VERSION)"; \
		exit 1; \
	fi
	@echo "  promtool   $(PROMTOOL_VERSION) OK"

##@ Build

build: verify ## Build the CI image
	$(CONTAINER_ENGINE) build --platform=$(PLATFORM) \
		$(foreach v,$(VERSION_ARGS),--build-arg $(v)=$($(v))) \
		-t $(IMAGE_NAME) .

##@ Test

test: build ## Build and smoke-test all tools in the image
	$(CONTAINER_ENGINE) run --rm $(IMAGE_NAME) bash -c '\
		echo "=== Tool Versions ===" && \
		echo "Go:        $$(go version)" && \
		echo "Azure CLI: $$(az version -o tsv 2>/dev/null | head -1)" && \
		echo "Bicep:     $$(bicep --version 2>&1)" && \
		echo "kubectl:   $$(kubectl version --client 2>&1 | head -1)" && \
		echo "kubelogin: $$(kubelogin --version 2>&1 | head -2)" && \
		echo "oc:        $$(oc version --client 2>&1 | head -1)" && \
		echo "promtool:  $$(promtool --version 2>&1 | head -1)" && \
		echo "make:      $$(make --version | head -1)" && \
		echo "git:       $$(git --version)" && \
		echo "=== All tools OK ==="'
