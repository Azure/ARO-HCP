SHELL = /bin/bash
DEPLOY_ENV ?= ntly
$(shell ../../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)

build:
	az acr create \
		--resource-group ${MGMT_RESOURCEGROUP} \
		--name ${ARO_HCP_IMAGE_ACR} \
		--sku Standard

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${ARO_HCP_IMAGE_ACR}:${COMMIT} .

push:
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker tag ${ARO_HCP_IMAGE_ACR}:${COMMIT} ${ARO_HCP_IMAGE_ACR}:latest
	docker push ${ARO_HCP_IMAGE_ACR}:${COMMIT}
	docker push ${ARO_HCP_IMAGE_ACR}:latest

.PHONY: build image push