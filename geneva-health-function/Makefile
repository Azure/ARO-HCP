GENEVA_HEALTH_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-env.mk
-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
GENEVA_HEALTH_IMAGE_TAG ?= $(or $(IMAGE_TAG),$(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh))
GENEVA_HEALTH_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${GENEVA_HEALTH_IMAGE_REPOSITORY} ../generate-repo.sh)
GENEVA_HEALTH_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(GENEVA_HEALTH_GENERATED_IMAGE_REPOSITORY):$(GENEVA_HEALTH_IMAGE_TAG)

.DEFAULT_GOAL := build-and-push

image:
	$(CONTAINER_ENGINE) build $(GENEVA_HEALTH_DIR) \
		--tag ${GENEVA_HEALTH_TAGGED_IMAGE}
.PHONY: image

build-and-push: $(ORAS) $(YQ)
	az acr build \
		--registry $(ARO_HCP_IMAGE_ACR) \
		--image $(GENEVA_HEALTH_GENERATED_IMAGE_REPOSITORY):$(GENEVA_HEALTH_IMAGE_TAG) \
		$(GENEVA_HEALTH_DIR)
.PHONY: build-and-push

# Unix timestamp for this make run; computed once so multiple targets see the same value.
MAKE_RUN_TS := $(shell date +%s)
OVERRIDE_CONFIG_FILE ?= /tmp/geneva-health-function-override-config-$(MAKE_RUN_TS).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
	$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
		--username 00000000-0000-0000-0000-000000000000 \
		--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${GENEVA_HEALTH_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.genevaHealthFunction.image.registry = \"$(ARO_HCP_IMAGE_REGISTRY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.genevaHealthFunction.image.repository = \"$(GENEVA_HEALTH_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.genevaHealthFunction.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

record-latest-override: $(YQ)
	@../hack/record-latest-image-override.sh \
		"$(ARO_HCP_IMAGE_ACR)" "$(ARO_HCP_IMAGE_REGISTRY)" \
		"$(GENEVA_HEALTH_GENERATED_IMAGE_REPOSITORY)" "$(DEPLOY_ENV)" \
		"genevaHealthFunction" "$(OVERRIDE_CONFIG_FILE)" "$(YQ)"
.PHONY: record-latest-override

deploy: build-and-push record-override
	make -C .. pipeline/GenevaHealthFunction OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy
