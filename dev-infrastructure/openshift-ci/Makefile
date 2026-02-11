SHELL = /bin/bash

include versions.mk

IMAGE_NAME      ?= aro-hcp-ci-test
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)

GO_WORK_VERSION := $(shell sed -n 's/^go //p' ../../go.work)

# All versioned ARGs that must stay in sync between versions.mk and the Dockerfile.
VERSION_ARGS = GO_INSTALL_VERSION KUBECTL_VERSION KUBELOGIN_VERSION PROMTOOL_VERSION

.PHONY: verify build test

##@ Verification

verify: ## Check versions.mk ↔ Dockerfile sync and Go major.minor constraint
	@fail=0; \
	for pair in $(foreach v,$(VERSION_ARGS),"$(v)=$($(v))"); do \
		name=$${pair%%=*}; expected=$${pair#*=}; \
		actual=$$(grep -oP "ARG $${name}=\K.*" Dockerfile); \
		if [ "$$actual" != "$$expected" ]; then \
			echo "ERROR: Dockerfile ARG $${name}=$$actual ≠ versions.mk $$expected"; \
			fail=1; \
		fi; \
	done; \
	mk_mm=$$(echo $(GO_INSTALL_VERSION) | cut -d. -f1,2); \
	gw_mm=$$(echo $(GO_WORK_VERSION) | cut -d. -f1,2); \
	if [ "$$mk_mm" != "$$gw_mm" ]; then \
		echo "ERROR: Go $(GO_INSTALL_VERSION) ($$mk_mm) ≠ go.work $(GO_WORK_VERSION) ($$gw_mm)"; \
		fail=1; \
	fi; \
	if [ $$fail -ne 0 ]; then exit 1; fi
	@echo "  Go         $(GO_INSTALL_VERSION) (go.work $(GO_WORK_VERSION)) OK"
	@echo "  kubectl    $(KUBECTL_VERSION)"
	@echo "  kubelogin  $(KUBELOGIN_VERSION)"
	@echo "  promtool   $(PROMTOOL_VERSION)"
	@echo "All checks passed."

##@ Build

build: verify ## Build the CI image
	$(CONTAINER_ENGINE) build \
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
