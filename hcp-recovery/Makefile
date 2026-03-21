SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec
HCP_RECOVERY_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

# Image
ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
HCP_RECOVERY_IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
HCP_RECOVERY_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${HCP_RECOVERY_IMAGE_REPOSITORY} ../generate-repo.sh)
HCP_RECOVERY_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(HCP_RECOVERY_GENERATED_IMAGE_REPOSITORY):$(HCP_RECOVERY_IMAGE_TAG)

HELMCHART_DIR = deploy
CONTAINER_TOOL ?= docker
BINARY = bin/hcp-recovery
HCP_RECOVERY_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')
LDFLAGS = -ldflags ""

# K8S Codegen
KUBE_CODEGEN_TAG=v0.34.3
KUBE_CODEGEN_SH=hack/kube-codegen.sh

# Default target
.DEFAULT_GOAL := build

$(KUBE_CODEGEN_SH):
	curl -o "$@" https://raw.githubusercontent.com/kubernetes/code-generator/refs/tags/${KUBE_CODEGEN_TAG}/kube_codegen.sh

update-codegen: $(KUBE_CODEGEN_SH) $(INFORMER_GEN) $(CLIENT_GEN) $(LISTER_GEN) $(INFORMER_GEN) $(APPLYCONFIGURATION_GEN)
	KUBE_CODEGEN_SH=$(KUBE_CODEGEN_SH) ./hack/update-codegen.sh
	make -C .. fmt
.PHONY: update-codegen

build: $(BINARY)
.PHONY: build

test:
	go test -v -timeout 300s -cover ./...
.PHONY: test

$(BINARY): $(HCP_RECOVERY_SOURCES)
	go build $(LDFLAGS) -o $(BINARY) .

run:
	go run . controller --namespace ${NAMESPACE} -v 2
.PHONY: run

image: Dockerfile $(HCP_RECOVERY_SOURCES)
	rm -f $(BINARY)
	cd $(HCP_RECOVERY_DIR)/.. && \
	$(CONTAINER_TOOL) build . --file hcp-recovery/Dockerfile \
		--platform linux/amd64 \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_IMAGE_TAG=${HCP_RECOVERY_IMAGE_TAG} \
		--tag ${HCP_RECOVERY_TAGGED_IMAGE}
	touch $@
.PHONY: image

build-and-push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	$(CONTAINER_TOOL) push ${HCP_RECOVERY_TAGGED_IMAGE}
.PHONY: build-and-push

OVERRIDE_CONFIG_FILE ?= /tmp/hcp-recovery-override-config-$(date +%s).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${HCP_RECOVERY_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.hcpRecovery.image.repository = \"$(HCP_RECOVERY_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.hcpRecovery.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

deploy: build-and-push record-override
	make -C .. pipeline/HcpRecovery OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy
