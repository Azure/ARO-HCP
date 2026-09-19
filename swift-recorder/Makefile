SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec
SWIFT_RECORDER_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-env.mk
-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_COMMIT_TIME ?= $(shell TZ=UTC git show -s --format=%cd --date=iso-strict-local ${ARO_HCP_REVISION})
SWIFT_RECORDER_IMAGE_TAG ?= $(or $(IMAGE_TAG),$(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh))
SWIFT_RECORDER_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${SWIFT_RECORDER_IMAGE_REPOSITORY} ../generate-repo.sh)
SWIFT_RECORDER_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(SWIFT_RECORDER_GENERATED_IMAGE_REPOSITORY):$(SWIFT_RECORDER_IMAGE_TAG)
BINARY = bin/swift-recorder
SWIFT_RECORDER_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')
LDFLAGS = -ldflags "-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_REVISION}"

.DEFAULT_GOAL := build

build: $(BINARY)
.PHONY: build

$(BINARY): $(SWIFT_RECORDER_SOURCES)
	go build $(LDFLAGS) -o $(BINARY) .

image:
	$(CONTAINER_ENGINE) build $(SWIFT_RECORDER_DIR)/.. --file $(SWIFT_RECORDER_DIR)/Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_COMMIT_TIME=${ARO_HCP_COMMIT_TIME} \
		--tag ${SWIFT_RECORDER_TAGGED_IMAGE}
.PHONY: image

build-and-push: image
	$(acr-login)
	$(CONTAINER_ENGINE) push ${SWIFT_RECORDER_TAGGED_IMAGE}
.PHONY: build-and-push

MAKE_RUN_TS := $(shell date +%s)
OVERRIDE_CONFIG_FILE ?= /tmp/swift-recorder-override-config-$(MAKE_RUN_TS).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 --password-stdin
	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${SWIFT_RECORDER_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.swiftRecorder.image.registry = \"$(ARO_HCP_IMAGE_REGISTRY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.swiftRecorder.image.repository = \"$(SWIFT_RECORDER_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.swiftRecorder.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

record-latest-override: $(YQ)
	@../hack/record-latest-image-override.sh \
		"$(ARO_HCP_IMAGE_ACR)" "$(ARO_HCP_IMAGE_REGISTRY)" \
		"$(SWIFT_RECORDER_IMAGE_REPOSITORY)" "$(DEPLOY_ENV)" \
		"swiftRecorder" "$(OVERRIDE_CONFIG_FILE)" "$(YQ)"
.PHONY: record-latest-override

deploy: build-and-push
	$(MAKE) record-override OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
	$(YQ) -i '.clouds.dev.environments.$(DEPLOY_ENV).defaults.swiftRecorder.enabled = true' $(OVERRIDE_CONFIG_FILE)
	$(MAKE) -C .. pipeline/SwiftRecorder OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy

test-deploy: $(TEMPLATIZE) $(YQ) $(HELM)
	TEMPLATIZE=$(abspath $(TEMPLATIZE)) YQ=$(YQ) HELM=$(HELM) bash testdata/test-deploy.sh
.PHONY: test-deploy
