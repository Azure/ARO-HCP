BACKEND_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
BACKEND_IMAGE_TAG ?= $(shell DETECT_DIRTY_GIT_WORKTREE=${DETECT_DIRTY_GIT_WORKTREE} DEPLOY_ENV=${DEPLOY_ENV} ../generate-tag.sh)
BACKEND_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${BACKEND_IMAGE_REPOSITORY} ../generate-repo.sh)
BACKEND_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(BACKEND_GENERATED_IMAGE_REPOSITORY):$(BACKEND_IMAGE_TAG)

# Binary name
BINARY = aro-hcp-backend

# Build flags
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -ldflags "\
	-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_REVISION}"

# Source files
BACKEND_SOURCES = $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum')

# Default target
.DEFAULT_GOAL := build

# Build the binary
build: $(BINARY)
.PHONY: build

$(BINARY): $(BACKEND_SOURCES)
	go build $(LDFLAGS) -o $(BINARY) .

run: $(BINARY)
	./$(BINARY) --location ${LOCATION} \
		--clusters-service-url http://localhost:8000 \
		--cosmos-name ${DB_NAME} \
		--cosmos-url ${DB_URL} \
		--namespace ${RP_NAMESPACE}
.PHONY: run

clean:
	rm -f ${BINARY}
.PHONY: clean

image: Dockerfile $(BACKEND_SOURCES) Makefile
	cd $(BACKEND_DIR)/.. && \
	docker build . --file backend/Dockerfile \
		--build-arg PLATFORM=linux/amd64 \
		--build-arg ARO_HCP_REVISION=${ARO_HCP_REVISION} \
		--build-arg ARO_HCP_IMAGE_TAG=${BACKEND_IMAGE_TAG} \
		--tag ${BACKEND_TAGGED_IMAGE}

build-and-push: image $(ORAS)
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${BACKEND_TAGGED_IMAGE}
.PHONY: build-and-push

# Unix timestamp for this make run; computed once so multiple targets see the same value.
MAKE_RUN_TS := $(shell date +%s)
OVERRIDE_CONFIG_FILE ?= /tmp/backend-override-config-$(MAKE_RUN_TS).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken | \
		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
			--username 00000000-0000-0000-0000-000000000000 \
			--password-stdin

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${BACKEND_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.backend.image.repository = \"$(BACKEND_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.backend.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE)
.PHONY: record-override

deploy: build-and-push record-override
	make -C .. pipeline/RP.Backend OVERRIDE_CONFIG_FILE=$(OVERRIDE_CONFIG_FILE)
.PHONY: deploy