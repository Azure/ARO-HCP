SHELL = /bin/bash

include versions.mk
include ../../setup-env.mk

IMAGE_NAME      ?= aro-hcp-ci-test
NESTED_PODMAN_IMAGE_NAME ?= aro-hcp-nested-podman-ci-test
PLATFORM ?= "linux/amd64"

GO_WORK_VERSION := $(shell sed -n 's/^go //p' ../../go.work)

VERSION_ARGS = BUILDER_IMAGE_TAG PROMTOOL_VERSION BICEP_VERSION

.PHONY: verify build test build-nested-podman test-nested-podman test-nested-podman-runtime update-tool-versions

##@ Verification

verify: ## Verify versions are consistent across config files
	@GO_WORK_MINOR=$$(echo "$(GO_WORK_VERSION)" | cut -d. -f1,2); \
	if ! BUILDER_GO=$$(echo "$(BUILDER_IMAGE_TAG)" | grep -oP 'golang-\K[0-9]+\.[0-9]+') || [ -z "$$BUILDER_GO" ]; then \
		echo "ERROR: cannot parse builder image Go version from tag '$(BUILDER_IMAGE_TAG)'"; \
		exit 1; \
	fi; \
	if [ "$${BUILDER_GO%%.*}" -lt "$${GO_WORK_MINOR%%.*}" ] || \
		{ [ "$${BUILDER_GO%%.*}" -eq "$${GO_WORK_MINOR%%.*}" ] && [ "$${BUILDER_GO#*.}" -lt "$${GO_WORK_MINOR#*.}" ]; }; then \
		echo "ERROR: builder image Go version $$BUILDER_GO is older than go.work Go version $$GO_WORK_MINOR"; \
		exit 1; \
	fi; \
	echo "  go version $$GO_WORK_MINOR OK"
	@actual=$$(grep -oP 'ARG PROMTOOL_VERSION=\K\S*' Dockerfile); \
	if [ "$$actual" != "$(PROMTOOL_VERSION)" ]; then \
		echo "ERROR: Dockerfile ARG PROMTOOL_VERSION=$$actual ≠ versions.mk $(PROMTOOL_VERSION)"; \
		exit 1; \
	fi
	@echo "  promtool   $(PROMTOOL_VERSION) OK"
	@actual=$$(grep -oP 'ARG BICEP_VERSION=\K\S*' Dockerfile); \
	if [ "$$actual" != "$(BICEP_VERSION)" ]; then \
		echo "ERROR: Dockerfile ARG BICEP_VERSION=$$actual ≠ versions.mk $(BICEP_VERSION)"; \
		exit 1; \
	fi
	@echo "  bicep      $(BICEP_VERSION) OK"

##@ Build

build: verify ## Build the CI image
	$(CONTAINER_ENGINE) build --platform=$(PLATFORM) \
		$(foreach v,$(VERSION_ARGS),--build-arg $(v)=$($(v))) \
		-f Dockerfile \
		-t $(IMAGE_NAME) ../..

build-nested-podman: ## Build the source-free nested Podman CI image
	$(CONTAINER_ENGINE) build --platform=$(PLATFORM) \
		$(if $(NESTED_PODMAN_IMAGE),--build-arg NESTED_PODMAN_IMAGE=$(NESTED_PODMAN_IMAGE)) \
		-f Dockerfile.nested-podman \
		-t $(NESTED_PODMAN_IMAGE_NAME) .

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
		echo "helm:      $$(helm version --short 2>&1)" && \
		echo "yq:        $$(yq --version 2>&1)" && \
		echo "jq:        $$(jq --version 2>&1)" && \
		echo "oras:      $$(oras version 2>&1 | head -1)" && \
		echo "=== All tools OK ==="'

test-nested-podman: build-nested-podman ## Smoke-test nested Podman tools and non-root Go builds
	$(CONTAINER_ENGINE) run --rm --entrypoint /bin/bash \
		$(NESTED_PODMAN_IMAGE_NAME) -euc '\
		test "$$(id -u)" = 1000 || { echo "ERROR: image must default to UID 1000" >&2; exit 1; }; \
		git --version; go version; make --version; \
		pass --version; qrencode --version; netstat --version; tree --version; podman --version; \
		export GOPATH=/opt/app-root; \
		d=$$(mktemp -d); cd "$$d"; \
		go mod init smoke.example.com/nonroot; \
		printf "package main\nfunc main() {}\n" > main.go; \
		go test .; go install .; "$$GOPATH/bin/nonroot"'

test-nested-podman-runtime: test-nested-podman ## Run a nested container (requires privileged container support)
	@output=$$($(CONTAINER_ENGINE) run --rm --privileged \
		$(NESTED_PODMAN_IMAGE_NAME) /bin/bash -euc '\
		test "$$(podman info --format "{{.Host.Security.Rootless}}")" = true || \
			{ echo "ERROR: Podman must run rootless" >&2; exit 1; }; \
		podman run --rm --network=host registry.access.redhat.com/ubi9/ubi-minimal:9.5 \
		/bin/echo nested-podman-ok') && \
		printf '%s\n' "$$output" && \
		{ test "$$output" = nested-podman-ok || \
			{ echo "ERROR: nested container did not produce the expected output" >&2; exit 1; }; }

##@ Maintenance

update-tool-versions: ## Bump every pinned tool in versions.mk/Dockerfile to its latest GitHub release
	./update-tool-versions.sh
