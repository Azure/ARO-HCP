SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]])
CONFIG_PROFILE ?= dev
include ../../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk
NAMESPACE ?= image-sync

deploy-shared:
	AZURE_SYNC_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n image-sync \
			--query clientId) && \
	helm upgrade --force --install shared-resources ./shared-resources \
	--namespace ${NAMESPACE} --create-namespace \
	--set serviceAccountName=image-sync \
	--set azureClientId=$${AZURE_SYNC_MI_CLIENT_ID}

deploy-component-sync:
	AZURE_SYNC_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n image-sync \
			--query clientId) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	helm upgrade --force --install component-sync-cronjob ./componentSyncCronjob \
	--namespace ${NAMESPACE} --create-namespace \
	--set serviceAccountName=image-sync \
	--set azureClientId=$${AZURE_SYNC_MI_CLIENT_ID} \
	--set acrRegistryName=${ARO_HCP_IMAGE_ACR} \
	--set azureTenantId=$${TENANT_ID} \
	--set componentSyncImage="arohcpdev.azurecr.io/image-sync/component-sync" \
	--set componentSyncTag=latest \
	--set credsBearerSecret=bearer-secret \
	--set credsPullSecret=component-pull-secret \
	--set credsKeyVaultName=service-kv-aro-hcp-dev \
	--set repositories=${REPOSITORIES_TO_SYNC}

deploy: deploy-shared deploy-component-sync

undeploy-shared:
	helm uninstall shared-resources --namespace ${NAMESPACE}

undeploy-component-sync:
	helm uninstall component-sync-cronjob --namespace ${NAMESPACE}

undeploy: undeploy-shared undeploy-component-sync

.PHONY: deploy-component-sync deploy-shared undeploy-shared undeploy-component-sync
