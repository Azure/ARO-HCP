FRONTEND_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
FRONTEND_IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
FRONTEND_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${FRONTEND_IMAGE_REPOSITORY} ../generate-repo.sh)
FRONTEND_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(FRONTEND_GENERATED_IMAGE_REPOSITORY):$(FRONTEND_IMAGE_TAG)

# Binary name
BINARY = aro-hcp-frontend

# Build flags
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -ldflags "\
	-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_REVISION}"

# Source files
FRONTEND_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

# Default target
.DEFAULT_GOAL := build

# Build the binary
build: $(BINARY)
.PHONY: build

$(BINARY): $(FRONTEND_SOURCES)
	go build $(LDFLAGS) -o $(BINARY) .

run: $(BINARY)
	./$(BINARY) --location ${LOCATION} \
		--clusters-service-url http://localhost:8000 \
		--cosmos-name ${DB_NAME} \
		--cosmos-url ${DB_URL}
.PHONY: run

clean:
	rm -f ${BINARY}
.PHONY: clean

image: Dockerfile $(FRONTEND_SOURCES) Makefile
	cd $(FRONTEND_DIR)/.. && \
	docker build . --file frontend/Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_IMAGE_TAG=${FRONTEND_IMAGE_TAG} \
		--tag ${FRONTEND_TAGGED_IMAGE}

build-and-push: image $(ORAS)
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${FRONTEND_TAGGED_IMAGE}
.PHONY: build-and-push

OVERRIDE_CONFIG_FILE ?= /tmp/frontend-override-config-$(date +%s).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
	$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
		--username 00000000-0000-0000-0000-000000000000 \
		--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${FRONTEND_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.frontend.image.repository = \"$(FRONTEND_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.frontend.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

deploy: build-and-push record-override
	make -C .. pipeline/RP.Frontend OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy

smoke-tests:
	go test -v -count 1 ./utils/frontend_smoke_test.go
