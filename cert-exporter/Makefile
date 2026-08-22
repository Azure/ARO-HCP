SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec
CERT_EXPORTER_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-env.mk
-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_COMMIT_TIME ?= $(shell TZ=UTC git show -s --format=%cd --date=iso-strict-local ${ARO_HCP_REVISION})
IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${CERT_EXPORTER_RBAC_CONTROLLER_IMAGE_REPOSITORY} ../generate-repo.sh)
TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(GENERATED_IMAGE_REPOSITORY):$(IMAGE_TAG)

BINARY = bin/rbac-controller
SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

.DEFAULT_GOAL := build

build: $(BINARY)
.PHONY: build

$(BINARY): $(SOURCES)
	go build -o $(BINARY) .

test:
	go test -v -timeout 300s -cover ./...
.PHONY: test

clean:
	rm -rf bin/
.PHONY: clean

image:
	rm -f $(BINARY)
	cd $(CERT_EXPORTER_DIR)/.. && \
	$(CONTAINER_ENGINE) build . --file cert-exporter/Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_COMMIT_TIME=${ARO_HCP_COMMIT_TIME} \
		--tag ${TAGGED_IMAGE}
.PHONY: image

build-and-push: image $(ORAS) $(YQ)
	$(acr-login)
	$(CONTAINER_ENGINE) push ${TAGGED_IMAGE}
.PHONY: build-and-push

MAKE_RUN_TS := $(shell date +%s)
OVERRIDE_CONFIG_FILE ?= /tmp/cert-exporter-override-config-$(MAKE_RUN_TS).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin
	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.certExporter.rbacController.image.registry = \"$(ARO_HCP_IMAGE_REGISTRY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.certExporter.rbacController.image.repository = \"$(GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.certExporter.rbacController.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override