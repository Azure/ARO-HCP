SHELL = /bin/bash

include versions.mk
include ../../setup-env.mk

IMAGE_NAME      ?= aro-hcp-ci-test
NESTED_PODMAN_IMAGE_NAME ?= aro-hcp-nested-podman-ci-test
PLATFORM ?= "linux/amd64"

GO_WORK_VERSION := $(shell sed -n 's/^go //p' ../../go.work)

VERSION_ARGS = BUILDER_IMAGE_TAG PROMTOOL_VERSION BICEP_VERSION
NESTED_PODMAN_VERSION_ARGS = GOTESTSUM_VERSION ENVTEST_VERSION

.PHONY: verify verify-nested-podman build test build-nested-podman test-nested-podman test-nested-podman-runtime update-tool-versions

##@ Verification

verify: verify-nested-podman ## Verify versions are consistent across config files
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

verify-nested-podman: ## Verify helper pins match the source-free image defaults
	@$(foreach v,$(NESTED_PODMAN_VERSION_ARGS),\
		actual=$$(grep -oP 'ARG $(v)=\K\S*' Dockerfile.nested-podman); \
		if [ "$$actual" != "$($(v))" ]; then \
			echo "ERROR: Dockerfile.nested-podman ARG $(v)=$$actual ≠ versions.mk $($(v))"; \
			exit 1; \
		fi; \
		echo "  $(v) $($(v)) OK";)

##@ Build

build: verify ## Build the CI image
	$(CONTAINER_ENGINE) build --platform=$(PLATFORM) \
		$(foreach v,$(VERSION_ARGS),--build-arg $(v)=$($(v))) \
		-f Dockerfile \
		-t $(IMAGE_NAME) ../..

build-nested-podman: verify-nested-podman ## Build the source-free nested Podman CI image
	$(CONTAINER_ENGINE) build --platform=$(PLATFORM) \
		$(foreach v,$(NESTED_PODMAN_VERSION_ARGS),--build-arg $(v)=$($(v))) \
		$(if $(NESTED_PODMAN_IMAGE),--build-arg NESTED_PODMAN_IMAGE=$(NESTED_PODMAN_IMAGE)) \
		-f Dockerfile.nested-podman \
		-t $(NESTED_PODMAN_IMAGE_NAME) .

##@ Test

test: $(if $(filter true,$(IN_CONTAINER)),verify,build) ## Build and smoke-test tools, or test the current CI image with IN_CONTAINER=true
ifeq ($(IN_CONTAINER),true)
	bash smoke-test.sh
else
	$(CONTAINER_ENGINE) run --rm -i $(IMAGE_NAME) bash -s < smoke-test.sh
endif

test-nested-podman: $(if $(filter true,$(IN_CONTAINER)),verify-nested-podman,build-nested-podman) ## Smoke-test nested Podman tools and non-root Go builds
	$(if $(filter true,$(IN_CONTAINER)),bash,$(CONTAINER_ENGINE) run --rm --entrypoint /bin/bash $(NESTED_PODMAN_IMAGE_NAME)) -euc '\
		test "$$(id -u)" = 1000 || { echo "ERROR: image must default to UID 1000" >&2; exit 1; }; \
		git --version; go version; make --version; \
		pass --version; qrencode --version; netstat --version; tree --version; podman --version; \
		gotestsum --version; \
		go version -m "$$(command -v gotestsum)" | grep -E "^[[:space:]]+mod[[:space:]]+gotest[.]tools/gotestsum[[:space:]]+v$(subst .,[.],$(GOTESTSUM_VERSION))[[:space:]]"; \
		go version -m "$$(command -v setup-envtest)" | grep -E "^[[:space:]]+mod[[:space:]]+sigs[.]k8s[.]io/controller-runtime/tools/setup-envtest[[:space:]]+$(subst .,[.],$(ENVTEST_VERSION))[[:space:]]"; \
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

update-tool-versions: ## Bump release-tracked tool pins; envtest compatibility pins are updated manually
	./update-tool-versions.sh
