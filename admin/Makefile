ADMIN_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ADMIN_API_IMAGE_TAG ?= $(shell DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
ADMIN_API_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${ADMIN_API_IMAGE_REPOSITORY} ../generate-repo.sh)
ADMIN_API_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(ADMIN_API_GENERATED_IMAGE_REPOSITORY):$(ADMIN_API_IMAGE_TAG)

# Binary name
BINARY = aro-hcp-admin

# Build flags
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -ldflags "\
	-X github.com/Azure/ARO-HCP/admin/pkg/version.commit=${ARO_HCP_REVISION} \
	-X github.com/Azure/ARO-HCP/admin/pkg/version.buildDate=${BUILD_DATE}"

# Source files
ADMIN_API_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

# Default target
.DEFAULT_GOAL := build

# Build the binary
build: $(BINARY)
.PHONY: build

$(BINARY): $(ADMIN_API_SOURCES)
	go build $(LDFLAGS) -o $(BINARY) .

run: $(BINARY)
	./${BINARY} --location ${LOCATION}
.PHONY: run

clean:
	rm -f ${BINARY}
.PHONY: clean

image: Dockerfile $(ADMIN_API_SOURCES) Makefile
	docker build . --file Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_IMAGE_TAG=${ADMIN_API_IMAGE_TAG} \
		--tag ${ADMIN_API_TAGGED_IMAGE}

build-and-push: image $(ORAS)
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${ADMIN_API_TAGGED_IMAGE}
.PHONY: build-and-push

OVERRIDE_CONFIG_FILE ?= /tmp/amin-api-override-config-$(date +%s).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${ADMIN_API_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.adminApi.image.repository = \"$(ADMIN_API_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.adminApi.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

deploy: build-and-push record-override
	make -C .. pipeline/AdminAPI OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy
