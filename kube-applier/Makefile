KUBE_APPLIER_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-env.mk
-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_COMMIT_TIME ?= $(shell TZ=UTC git show -s --format=%cd --date=iso-strict-local ${ARO_HCP_REVISION})
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
KUBE_APPLIER_IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
KUBE_APPLIER_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${KUBE_APPLIER_IMAGE_REPOSITORY} ../generate-repo.sh)
KUBE_APPLIER_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(KUBE_APPLIER_GENERATED_IMAGE_REPOSITORY):$(KUBE_APPLIER_IMAGE_TAG)

# Binary name
BINARY = kube-applier

# Build flags
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

# Source files
BACKEND_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

# Default target
.DEFAULT_GOAL := build

# Build the binary
build: $(BINARY)
.PHONY: build

$(BINARY): $(BACKEND_SOURCES)
	go build -o $(BINARY) .

clean:
	rm -f ${BINARY}
.PHONY: clean

image:
	cd $(KUBE_APPLIER_DIR)/.. && \
	$(CONTAINER_ENGINE) build . --file kube-applier/Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_COMMIT_TIME=${ARO_HCP_COMMIT_TIME} \
		--tag ${KUBE_APPLIER_TAGGED_IMAGE}
.PHONY: image

build-and-push: image $(ORAS) $(YQ)
	$(acr-login)
	@LOCAL_DIGEST=$$($(CONTAINER_ENGINE) image inspect --format '{{.Id}}' ${KUBE_APPLIER_TAGGED_IMAGE} | sed 's/sha256://'); \
	REMOTE_DIGEST=$$($(ORAS) manifest fetch ${KUBE_APPLIER_TAGGED_IMAGE} 2>/dev/null | $(YQ) .config.digest | sed 's/sha256://'); \
	if [ "$$LOCAL_DIGEST" = "$$REMOTE_DIGEST" ]; then \
		echo "Image ${KUBE_APPLIER_TAGGED_IMAGE} already up to date in registry, skipping push"; \
	else \
		$(CONTAINER_ENGINE) push ${KUBE_APPLIER_TAGGED_IMAGE}; \
	fi
.PHONY: build-and-push

# Unix timestamp for this make run; computed once so multiple targets see the same value.
MAKE_RUN_TS := $(shell date +%s)
OVERRIDE_CONFIG_FILE ?= /tmp/kube-applier-override-config-$(MAKE_RUN_TS).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${KUBE_APPLIER_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.kubeApplier.image.registry = \"$(ARO_HCP_IMAGE_REGISTRY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.kubeApplier.image.repository = \"$(KUBE_APPLIER_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.kubeApplier.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

record-latest-override: $(YQ)
	@../hack/record-latest-image-override.sh \
		"$(ARO_HCP_IMAGE_ACR)" "$(ARO_HCP_IMAGE_REGISTRY)" \
		"$(KUBE_APPLIER_GENERATED_IMAGE_REPOSITORY)" "$(DEPLOY_ENV)" \
		"kubeApplier" "$(OVERRIDE_CONFIG_FILE)" "$(YQ)"
.PHONY: record-latest-override

deploy: build-and-push record-override
	make -C .. pipeline/KubeApplier OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy