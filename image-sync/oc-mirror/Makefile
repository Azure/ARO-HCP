SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]])
CONFIG_PROFILE ?= dev
include ../../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk
ARO_HCP_FULL ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
OC_MIRROR_IMAGE ?= $(ARO_HCP_FULL)/image-sync/ocmirror
OC_MIRROR_IMAGE_TAGGED ?= $(OC_MIRROR_IMAGE):$(COMMIT)

build-push: image push

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${OC_MIRROR_IMAGE_TAGGED} .

push: image
	docker tag ${OC_MIRROR_IMAGE_TAGGED} ${OC_MIRROR_IMAGE}:latest
	docker push ${OC_MIRROR_IMAGE_TAGGED}
	docker push ${OC_MIRROR_IMAGE}:latest

deploy:
	AZURE_SYNC_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n image-sync \
			--query clientId) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	oc process --local \
	 -f deploy/image-sync-template.yml \
	 -p OC_MIRROR_IMAGE=arohcpdev.azurecr.io/image-sync/ocmirror:latest \
	 -p OC_MIRROR_CLIENT_ID=$${AZURE_SYNC_MI_CLIENT_ID} \
	 -p TENANT_ID=$${TENANT_ID} \
	 -p KEYVAULT_NAME=service-kv-aro-hcp-dev \
	 -p TARGET_REGISTRY=${ARO_HCP_IMAGE_ACR} \
	 -p PULL_SECRET_NAME=pull-secret \
	 -p NAMESPACE=image-sync  -o yaml | kubectl delete -f -

.PHONY: image push build-push deploy
