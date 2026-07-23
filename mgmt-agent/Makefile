SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec
MGMT_AGENT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-env.mk
-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

# Image
ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_COMMIT_TIME ?= $(shell TZ=UTC git show -s --format=%cd --date=iso-strict-local ${ARO_HCP_REVISION})
MGMT_AGENT_IMAGE_TAG ?= $(or $(IMAGE_TAG),$(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh))
MGMT_AGENT_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${MGMT_AGENT_IMAGE_REPOSITORY} ../generate-repo.sh)
MGMT_AGENT_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(MGMT_AGENT_GENERATED_IMAGE_REPOSITORY):$(MGMT_AGENT_IMAGE_TAG)

HELMCHART_DIR = deploy
BINARY = bin/mgmt-agent
MGMT_AGENT_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')
LDFLAGS = -ldflags "\
	-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_REVISION}"

# Default target
.DEFAULT_GOAL := build

build: $(BINARY)
.PHONY: build

$(BINARY): $(MGMT_AGENT_SOURCES)
	go build $(LDFLAGS) -o $(BINARY) .

run:
	go run . controller --namespace ${NAMESPACE} --log-verbosity 2
.PHONY: run

image:
	rm -f $(BINARY)
	cd $(MGMT_AGENT_DIR)/.. && \
	$(CONTAINER_ENGINE) build . --file mgmt-agent/Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_COMMIT_TIME=${ARO_HCP_COMMIT_TIME} \
		--tag ${MGMT_AGENT_TAGGED_IMAGE}
.PHONY: image

build-and-push: image $(ORAS) $(YQ)
	$(acr-login)
	@LOCAL_DIGEST=$$($(CONTAINER_ENGINE) image inspect --format '{{.Id}}' ${MGMT_AGENT_TAGGED_IMAGE} | sed 's/sha256://'); \
	REMOTE_DIGEST=$$($(ORAS) manifest fetch ${MGMT_AGENT_TAGGED_IMAGE} 2>/dev/null | $(YQ) .config.digest | sed 's/sha256://'); \
	if [ "$$LOCAL_DIGEST" = "$$REMOTE_DIGEST" ]; then \
		echo "Image ${MGMT_AGENT_TAGGED_IMAGE} already up to date in registry, skipping push"; \
	else \
		$(CONTAINER_ENGINE) push ${MGMT_AGENT_TAGGED_IMAGE}; \
	fi
.PHONY: build-and-push

# Unix timestamp for this make run; computed once so multiple targets see the same value.
MAKE_RUN_TS := $(shell date +%s)
OVERRIDE_CONFIG_FILE ?= /tmp/mgmt-agent-override-config-$(MAKE_RUN_TS).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${MGMT_AGENT_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.mgmtAgent.image.registry = \"$(ARO_HCP_IMAGE_REGISTRY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.mgmtAgent.image.repository = \"$(MGMT_AGENT_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.mgmtAgent.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

# Dev work

record-latest-override: $(YQ)
	@../hack/record-latest-image-override.sh \
		"$(ARO_HCP_IMAGE_ACR)" "$(ARO_HCP_IMAGE_REGISTRY)" \
		"$(MGMT_AGENT_GENERATED_IMAGE_REPOSITORY)" "$(DEPLOY_ENV)" \
		"mgmtAgent" "$(OVERRIDE_CONFIG_FILE)" "$(YQ)"
.PHONY: record-latest-override

deploy: build-and-push record-override
	make -C .. pipeline/MgmtAgent OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy
