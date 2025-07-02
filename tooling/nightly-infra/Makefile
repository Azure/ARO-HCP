SHELL = /bin/bash
DEPLOY_ENV ?= ntly
$(shell ../../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)

# Container configuration
IMAGE = ${ARO_HCP_IMAGE_ACR}.azurecr.io/ntly-infra
TAG = ${COMMIT}

# Build container image
image:
	docker build --platform="linux/amd64" -t ${IMAGE}:${TAG} .
	docker tag ${IMAGE}:${TAG} ${IMAGE}:latest

# Push to registry
push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${IMAGE}:${TAG}
	docker push ${IMAGE}:latest

# Deploy infrastructure
deploy:
	chmod +x deploy-infra.sh
	./deploy-infra.sh rg-aro-hcp-${DEPLOY_ENV} uksouth ${ARO_HCP_IMAGE_ACR} ${DEPLOY_ENV}

# Build and deploy everything
all: push deploy

# Show configuration
info:
	@echo "Environment: ${DEPLOY_ENV}"
	@echo "Registry: ${ARO_HCP_IMAGE_ACR}"
	@echo "Image: ${IMAGE}:${TAG}"
	@echo "Service RG: ${SVC_RESOURCEGROUP}"
	@echo "Management RG: ${MGMT_RESOURCEGROUP}"

.PHONY: image push deploy all info