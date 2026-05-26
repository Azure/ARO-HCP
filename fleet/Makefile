FLEET_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)

-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_COMMIT_TIME ?= $(shell TZ=UTC git show -s --format=%cd --date=iso-strict-local ${ARO_HCP_REVISION})
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
FLEET_IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
FLEET_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${FLEET_IMAGE_REPOSITORY} ../generate-repo.sh)
FLEET_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(FLEET_GENERATED_IMAGE_REPOSITORY):$(FLEET_IMAGE_TAG)

BINARY = fleet
LDFLAGS = -ldflags "\
	-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_REVISION}"
FLEET_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

.DEFAULT_GOAL := build

build: $(BINARY)
.PHONY: build

$(BINARY): $(FLEET_SOURCES)
	go build $(LDFLAGS) -o $(BINARY) .

clean:
	rm -f $(BINARY)
.PHONY: clean

image:
	cd $(FLEET_DIR)/.. && \
	$(CONTAINER_ENGINE) build . --file fleet/Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_COMMIT_TIME=${ARO_HCP_COMMIT_TIME} \
		--tag ${FLEET_TAGGED_IMAGE}
.PHONY: image

build-and-push: image $(ORAS) $(YQ)
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	@LOCAL_DIGEST=$$($(CONTAINER_ENGINE) image inspect --format '{{.Id}}' ${FLEET_TAGGED_IMAGE} | sed 's/sha256://'); \
	REMOTE_DIGEST=$$($(ORAS) manifest fetch ${FLEET_TAGGED_IMAGE} 2>/dev/null | $(YQ) .config.digest | sed 's/sha256://'); \
	if [ "$$LOCAL_DIGEST" = "$$REMOTE_DIGEST" ]; then \
		echo "Image ${FLEET_TAGGED_IMAGE} already up to date in registry, skipping push"; \
	else \
		$(CONTAINER_ENGINE) push ${FLEET_TAGGED_IMAGE}; \
	fi
.PHONY: build-and-push

MAKE_RUN_TS := $(shell date +%s)
OVERRIDE_CONFIG_FILE ?= /tmp/fleet-override-config-$(MAKE_RUN_TS).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${FLEET_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.fleet.image.registry = \"$(ARO_HCP_IMAGE_REGISTRY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.fleet.image.repository = \"$(FLEET_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.fleet.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override
