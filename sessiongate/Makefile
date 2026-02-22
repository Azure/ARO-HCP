SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec
SESSIONGATE_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk
-include ../tooling/hcpctl/Variables.mk

# Image
ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
SESSION_GATE_IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
SESSION_GATE_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${SESSION_GATE_IMAGE_REPOSITORY} ../generate-repo.sh)
SESSION_GATE_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(SESSION_GATE_GENERATED_IMAGE_REPOSITORY):$(SESSION_GATE_IMAGE_TAG)

HELMCHART_DIR = deploy
CONTAINER_TOOL ?= docker
BINARY = bin/sessiongate
SESSION_GATE_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')
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

$(BINARY): $(SESSION_GATE_SOURCES)
	go build $(LDFLAGS) -o $(BINARY) .

run:
	go run . controller --namespace ${NAMESPACE} --ingress-base-url "http://localhost:8080" -v 2
.PHONY: run

image: Dockerfile $(SESSION_GATE_SOURCES)
	rm -f $(BINARY)
	cd $(SESSIONGATE_DIR)/.. && \
	$(CONTAINER_TOOL) build . --file sessiongate/Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_IMAGE_TAG=${SESSION_GATE_IMAGE_TAG} \
		--tag ${SESSION_GATE_TAGGED_IMAGE}
	touch $@

build-and-push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	$(CONTAINER_TOOL) push ${SESSION_GATE_TAGGED_IMAGE}
.PHONY: build-and-push

OVERRIDE_CONFIG_FILE ?= /tmp/sessiongate-override-config-$(date +%s).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${SESSION_GATE_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.sessiongate.image.repository = \"$(SESSION_GATE_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.sessiongate.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

# Dev work

deploy: build-and-push record-override
	make -C .. pipeline/SessionGate OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy
