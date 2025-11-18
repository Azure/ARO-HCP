SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

# Image
ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
SESSION_GATE_IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
SESSION_GATE_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${SESSION_GATE_IMAGE_REPOSITORY} ../generate-repo.sh)
SESSION_GATE_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(SESSION_GATE_GENERATED_IMAGE_REPOSITORY):$(SESSION_GATE_IMAGE_TAG)

HELMCHART_DIR = deploy
CONTAINER_TOOL ?= docker
BINARY = bin/manager
SESSION_GATE_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')
LDFLAGS = -ldflags ""

# Default target
.DEFAULT_GOAL := build

manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) rbac:roleName=sessiongate-controller crd webhook paths="./..." output:dir=$(HELMCHART_DIR)/templates output:crd:artifacts:config=$(HELMCHART_DIR)/templates
	mv $(HELMCHART_DIR)/templates/role.yaml $(HELMCHART_DIR)/templates/controller_role.yaml
.PHONY: manifests

generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."
.PHONY: generate

build: manifests generate $(BINARY)
.PHONY: build

$(BINARY): $(SESSION_GATE_SOURCES)
	go build $(LDFLAGS) -o $(BINARY) cmd/main.go

run: $(BINARY)
	go run ./cmd/main.go
.PHONY: run

image: Dockerfile $(SESSION_GATE_SOURCES)
	$(CONTAINER_TOOL) build . --file Dockerfile \
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

deploy: build-and-push record-override
	make -C .. pipeline/SessionGate OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy