SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
CONFIG_PROFILE ?= dev
include ../../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_IMAGE_SYNC_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/image-sync/component-sync

image-sync:
	go build -tags=containers_image_openpgp,requirefips .

run:
	go run ./... -c example.yml

clean:
	rm -f aro-hcp-frontend

build-push: image push

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${ARO_HCP_IMAGE_SYNC_IMAGE}:${COMMIT} .

push:
	docker tag ${ARO_HCP_IMAGE_SYNC_IMAGE}:${COMMIT} ${ARO_HCP_IMAGE_SYNC_IMAGE}:latest
	docker push ${ARO_HCP_IMAGE_SYNC_IMAGE}:${COMMIT}
	docker push ${ARO_HCP_IMAGE_SYNC_IMAGE}:latest

deploy:
	AZURE_SYNC_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n image-sync \
			--query clientId) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	oc process --local \
	 -f deploy/image-sync-template.yml \
	 -p COMPONENT_SYNC_IMAGE=arohcpdev.azurecr.io/image-sync/component-sync:latest \
	 -p IMAGE_SYNC_CLIENT_ID=$${AZURE_SYNC_MI_CLIENT_ID} \
	 -p TENANT_ID=$${TENANT_ID} \
	 -p KEYVAULT_NAME=service-kv-aro-hcp-dev \
	 -p TARGET_REGISTRY=${ARO_HCP_IMAGE_ACR} \
	 -p PULL_SECRET_NAME=pull-secret \
	 -p QUAY_BEARER_SECRET_NAME=bearer-secret \
	 -p NAMESPACE=image-sync  -o yaml > deploy.yml

.PHONY: image-sync clean image run deploy


#| kubectl apply -f -