SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]])
CONFIG_PROFILE ?= dev
include ../../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk


deploy:
	AZURE_SYNC_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n image-sync \
			--query clientId) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	oc process --local \
	 -f deploy/common-template.yml \
	 -p OC_MIRROR_CLIENT_ID=$${AZURE_SYNC_MI_CLIENT_ID} \
	 -p NAMESPACE=image-sync  -o yaml | kubectl apply -f -

.PHONY:  deploy
