TENANT_QUOTA_COLLECTOR_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

DEPLOY_ENV ?= opstool

-include $(TENANT_QUOTA_COLLECTOR_DIR)/../../../setup-templatize-env.mk
-include $(TENANT_QUOTA_COLLECTOR_DIR)/../../../.bingo/Variables.mk

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_ACR ?= arohcpsvcdev
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
TENANT_QUOTA_COLLECTOR_IMAGE_REPOSITORY ?= tenant-quota-collector

# Tag defaults to 'latest' for opstool dev environment (matches config-opstool.yaml)
TENANT_QUOTA_IMAGE_REPOSITORY ?= $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${TENANT_QUOTA_COLLECTOR_IMAGE_REPOSITORY} $(TENANT_QUOTA_COLLECTOR_DIR)/../../../generate-repo.sh)
TENANT_QUOTA_IMAGE_TAG ?= latest
TENANT_QUOTA_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(TENANT_QUOTA_IMAGE_REPOSITORY):$(TENANT_QUOTA_IMAGE_TAG)

ifeq ($(ARO_HCP_IMAGE_REGISTRY:%.azurecr.io=azurecr.io),azurecr.io)
ARO_HCP_IMAGE_REGISTRY_IS_ACR = 1
endif

.DEFAULT_GOAL := tenant-quota-collector

tenant-quota-collector:
	go build -ldflags="-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${TENANT_QUOTA_IMAGE_TAG}" -o tenant-quota-collector .

run:
	CONFIG_PATH=./deploy/templates/configmap.yaml ./tenant-quota-collector

clean:
	rm -f tenant-quota-collector

image: Dockerfile $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum') Makefile
	cd $(TENANT_QUOTA_COLLECTOR_DIR)/../../.. && \
	docker build . --file dev-infrastructure/ops-tools/tenant-quota/Dockerfile \
	               --build-arg PLATFORM=linux/amd64 \
	               --build-arg REVISION=${ARO_HCP_REVISION} \
	               --build-arg TAG=${TENANT_QUOTA_IMAGE_TAG} \
	               --tag ${TENANT_QUOTA_TAGGED_IMAGE}
.PHONY: image

build-and-push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${TENANT_QUOTA_TAGGED_IMAGE}
	@echo ""
	@echo "Pushed: ${TENANT_QUOTA_TAGGED_IMAGE}"
.PHONY: build-and-push

show-image:
	@echo "Image: ${TENANT_QUOTA_TAGGED_IMAGE}"
	@echo "Repository: ${TENANT_QUOTA_IMAGE_REPOSITORY}"
	@echo "Tag: ${TENANT_QUOTA_IMAGE_TAG}"
.PHONY: show-image

# Deployment is now handled by templatize:
#   ./templatize-bin pipeline run \
#     --service-group Microsoft.Azure.ARO.HCP.Opstool.TenantQuota \
#     --topology-file topology-opstool.yaml \
#     --config-file config/config-opstool.yaml \
#     --dev-settings-file tooling/templatize/settings.yaml \
#     --dev-environment opstool
