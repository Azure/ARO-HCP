.PHONY: build update update-repositories clean test test-verbose test-coverage image build-and-push image-digest help

export AZURE_TOKEN_CREDENTIALS ?= dev

IMAGE_UPDATER_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include $(IMAGE_UPDATER_DIR)/../../setup-env.mk

BINARY_NAME := image-updater
CONFIG_FILE := config.yaml
COMPONENTS ?=
GROUPS ?=
EXCLUDE_COMPONENTS ?=
VERBOSITY ?= 0
OUTPUT_FILE ?=
OUTPUT_FORMAT ?= markdown
VERBOSITY_FLAGS := $(if $(VERBOSITY),--verbosity $(VERBOSITY))
COMPONENT_FLAGS := $(if $(COMPONENTS),--components $(COMPONENTS))
GROUP_FLAGS := $(if $(GROUPS),--groups $(GROUPS))
EXCLUDE_FLAGS := $(if $(EXCLUDE_COMPONENTS),--exclude-components $(EXCLUDE_COMPONENTS))
OUTPUT_FLAGS := $(if $(OUTPUT_FILE),--output-file $(OUTPUT_FILE)) $(if $(OUTPUT_FORMAT),--output-format $(OUTPUT_FORMAT))

# The monitor image follows the tenant-quota-collector convention: built and
# pushed by hand from a checkout, then pinned by digest in
# config/config-dev-ci.yaml (opstool.imageUpdaterMonitor.image.digest).
DEPLOY_ENV ?= dev-ci
ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_ACR ?= arohcpsvcdev
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
IMAGE_UPDATER_MONITOR_REPOSITORY ?= image-updater-monitor
IMAGE_UPDATER_MONITOR_REPO ?= $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${IMAGE_UPDATER_MONITOR_REPOSITORY} $(IMAGE_UPDATER_DIR)/../../generate-repo.sh)
IMAGE_UPDATER_IMAGE_TAG ?= latest
IMAGE_UPDATER_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(IMAGE_UPDATER_MONITOR_REPO):$(IMAGE_UPDATER_IMAGE_TAG)

.DEFAULT_GOAL := help

build:
	@go build -o $(BINARY_NAME) .

update: build
	@./$(BINARY_NAME) update --config $(CONFIG_FILE) --tags $(COMPONENT_FLAGS) $(GROUP_FLAGS) $(EXCLUDE_FLAGS) $(VERBOSITY_FLAGS) $(OUTPUT_FLAGS)

update-repositories: build
	@./$(BINARY_NAME) update --config $(CONFIG_FILE) --repositories $(VERBOSITY_FLAGS) $(OUTPUT_FLAGS)

test:
	@go test ./...

test-verbose:
	@go test ./... -v

test-coverage:
	@go test ./... -cover

clean:
	rm -f $(BINARY_NAME)

# ---------------------------------------------------------------------------
# Monitor service image (deployed to the opstool cluster in dev CI)
# ---------------------------------------------------------------------------
image: Dockerfile $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum') Makefile
	cd $(IMAGE_UPDATER_DIR)/../.. && \
	$(CONTAINER_ENGINE) build . --file tooling/image-updater/Dockerfile \
	               --build-arg PLATFORM=linux/amd64 \
	               --build-arg REVISION=${ARO_HCP_REVISION} \
	               --tag ${IMAGE_UPDATER_TAGGED_IMAGE}
.PHONY: image

build-and-push: image
	$(acr-login)
	$(CONTAINER_ENGINE) push ${IMAGE_UPDATER_TAGGED_IMAGE}
	@echo ""
	@echo "Pushed: ${IMAGE_UPDATER_TAGGED_IMAGE}"
	@echo "Pin it in config/config-dev-ci.yaml (opstool.imageUpdaterMonitor.image.digest) using: make image-digest"
.PHONY: build-and-push

image-digest:
	@az acr manifest list-metadata --name $(IMAGE_UPDATER_MONITOR_REPO) --registry $(ARO_HCP_IMAGE_ACR) \
	  --query "sort_by([?tags != null && contains(tags, '$(IMAGE_UPDATER_IMAGE_TAG)')], &lastUpdateTime)[-1].digest" -o tsv
.PHONY: image-digest

help:
	@echo "Available targets:"
	@echo "  build              - Build the image-updater binary"
	@echo "  update             - Build and run the updater (tags/digests mode)"
	@echo "  update-repositories - Check and update repository versions"
	@echo "  test               - Run tests"
	@echo "  test-verbose       - Run tests with verbose output"
	@echo "  test-coverage      - Run tests with coverage"
	@echo "  image              - Build the monitor service container image"
	@echo "  build-and-push     - Build and push the monitor image to the ACR"
	@echo "  image-digest       - Print the digest of the pushed monitor image (for config pinning)"
	@echo "  clean              - Remove built binary"
	@echo ""
	@echo "Variables:"
	@echo "  COMPONENTS         - Comma-separated list of components to update"
	@echo "  GROUPS             - Comma-separated list of groups to update (e.g., hypershift-stack,velero)"
	@echo "  EXCLUDE_COMPONENTS - Comma-separated list of components to exclude"
	@echo "  VERBOSITY          - Set verbosity level (default: 0, 0-1: summary, 2+: debug)"
	@echo "  OUTPUT_FILE        - Write results to file instead of stdout"
	@echo "  OUTPUT_FORMAT      - Output format: table, markdown, json (default: table)"
	@echo ""
	@echo "Examples:"
	@echo "  make update VERBOSITY=2"
	@echo "  make update COMPONENTS=hypershift,secretSyncController"
	@echo "  make update GROUPS=hypershift-stack,velero"
	@echo "  make update GROUPS=hypershift-stack EXCLUDE_COMPONENTS=hypershift-shared-ingress"
	@echo "  make update EXCLUDE_COMPONENTS=secretSyncController VERBOSITY=1"
	@echo "  make update OUTPUT_FILE=results.md OUTPUT_FORMAT=markdown"
	@echo "  make update-repositories"
	@echo "  make update-repositories VERBOSITY=2"
