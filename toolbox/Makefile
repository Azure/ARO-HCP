TOOLBOX_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-env.mk
-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_COMMIT_TIME ?= $(shell TZ=UTC git show -s --format=%cd --date=iso-strict-local ${ARO_HCP_REVISION})
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
TOOLBOX_IMAGE_TAG ?= $(or $(IMAGE_TAG),$(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh))
TOOLBOX_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${TOOLBOX_IMAGE_REPOSITORY} ../generate-repo.sh)
TOOLBOX_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(TOOLBOX_GENERATED_IMAGE_REPOSITORY):$(TOOLBOX_IMAGE_TAG)

# kubectl release shipped in the toolbox image. Keep within one minor of the
# management-cluster AKS version (kubectl client/server skew support).
KUBECTL_VERSION ?= v1.36.6

# Default target
.DEFAULT_GOAL := image

image:
	$(CONTAINER_ENGINE) build $(TOOLBOX_DIR) \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg KUBECTL_VERSION=${KUBECTL_VERSION} \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_COMMIT_TIME=${ARO_HCP_COMMIT_TIME} \
		--tag ${TOOLBOX_TAGGED_IMAGE}
.PHONY: image

build-and-push: image $(ORAS) $(YQ)
	$(acr-login)
	@LOCAL_DIGEST=$$($(CONTAINER_ENGINE) image inspect --format '{{.Id}}' ${TOOLBOX_TAGGED_IMAGE} | sed 's/sha256://'); \
	REMOTE_DIGEST=$$($(ORAS) manifest fetch ${TOOLBOX_TAGGED_IMAGE} 2>/dev/null | $(YQ) .config.digest | sed 's/sha256://'); \
	if [ "$$LOCAL_DIGEST" = "$$REMOTE_DIGEST" ]; then \
		echo "Image ${TOOLBOX_TAGGED_IMAGE} already up to date in registry, skipping push"; \
	else \
		$(CONTAINER_ENGINE) push ${TOOLBOX_TAGGED_IMAGE}; \
	fi
.PHONY: build-and-push

# Unix timestamp for this make run; computed once so multiple targets see the same value.
MAKE_RUN_TS := $(shell date +%s)
OVERRIDE_CONFIG_FILE ?= /tmp/toolbox-override-config-$(MAKE_RUN_TS).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${TOOLBOX_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.toolbox.image.registry = \"$(ARO_HCP_IMAGE_REGISTRY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.toolbox.image.repository = \"$(TOOLBOX_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.toolbox.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

record-latest-override: $(YQ)
	@../hack/record-latest-image-override.sh \
		"$(ARO_HCP_IMAGE_ACR)" "$(ARO_HCP_IMAGE_REGISTRY)" \
		"$(TOOLBOX_IMAGE_REPOSITORY)" "$(DEPLOY_ENV)" \
		"toolbox" "$(OVERRIDE_CONFIG_FILE)" "$(YQ)"
.PHONY: record-latest-override
