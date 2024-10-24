SHELL = /bin/bash

include configurations/config.mk
include Makefile.lint.mk

# Set SKIP_CONFIRM to a non-empty value to skip "what-if" confirmation prompts.
ifndef SKIP_CONFIRM
PROMPT_TO_CONFIRM = "--confirm-with-what-if"
endif

list:
	@grep '^[^#[:space:]].*:' Makefile
.PHONY: list

feature-registration: # hardcoded to eastus as this is a subscription deployment, not a resource group
	@az deployment sub create \
		--name "feature-registration" \
		--location eastus \
		--template-file templates/feature-registration.bicep \
		$(PROMPT_TO_CONFIRM)
.PHONY: feature-registration

rg:
	@if [ "$$(az group exists --name $(RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: rg

cleanup-orphaned-rolebindings:
	@scripts/cleanup-orphaned-rolebindings.sh $(SVC_RESOURCEGROUP)
	@scripts/cleanup-orphaned-rolebindings.sh $(MGMT_RESOURCEGROUP)
	@scripts/cleanup-orphaned-rolebindings.sh $(REGIONAL_RESOURCEGROUP)
	@scripts/cleanup-orphaned-rolebindings.sh ${GLOBAL_RESOURCEGROUP}
.PHONY: cleanup-orphaned-rolebindings

create-mock-identities:
	az deployment group create \
		--name "aro-hcp-dev-mock-certificates" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/mock-identities.bicep $(PROMPT_TO_CONFIRM) \
		--parameters configurations/mock-identities.bicepparam
	APPLICATION_NAME=aro-dev-first-party \
	KEY_VAULT_NAME=aro-hcp-dev-svc-kv \
	CERTIFICATE_NAME=firstPartyCert \
	ROLE_DEFINITION_NAME=dev-first-party-mock \
	SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) \
	./scripts/create-sp-for-rbac.sh

	app_id=$(shell az ad sp list --display-name aro-dev-first-party --query "[].appId" -o tsv) && \
	subscription_id=$(shell az account show --query id --output tsv) && \
	az role assignment create --role Contributor --assignee $${app_id} --scope "/subscriptions/$${subscription_id}"

	APPLICATION_NAME=aro-dev-arm-helper \
	KEY_VAULT_NAME=aro-hcp-dev-svc-kv \
	CERTIFICATE_NAME=armHelperCert \
	ROLE_DEFINITION_NAME='Role Based Access Control Administrator' \
	SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) \
	./scripts/create-sp-for-rbac.sh

	APPLICATION_NAME=aro-dev-msi-mock \
	KEY_VAULT_NAME=aro-hcp-dev-svc-kv \
	CERTIFICATE_NAME=msiMockCert \
	ROLE_DEFINITION_NAME=Contributor \
	SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) \
	./scripts/create-sp-for-rbac.sh
.PHONY: create-mock-identities

#
# Image Sync
#

imagesyncRg:
	@if [ "$$(az group exists --name $(IMAGE_SYNC_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(IMAGE_SYNC_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: imagesyncRg

imagesync: imagesyncRg
	az deployment group create \
		--name "image-sync" \
		--resource-group $(IMAGE_SYNC_RESOURCEGROUP) \
		--template-file templates/image-sync.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/image-sync.bicepparam
.PHONY: imagesync

imagesync.what-if: imagesyncRg
	az deployment group what-if \
		--name "image-sync" \
		--resource-group $(IMAGE_SYNC_RESOURCEGROUP) \
		--template-file templates/image-sync.bicep \
		--parameters \
			configurations/image-sync.bicepparam
.PHONY: imagesync.what-if

#
# Region
#

regionalRg:
	@if [ "$$(az group exists --name $(REGIONAL_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(REGIONAL_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: regionalRg

region: regionalRg
	az deployment group create \
		--name "region" \
		--resource-group $(REGIONAL_RESOURCEGROUP) \
		--template-file templates/region.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/region.bicepparam
.PHONY: region

region.what-if: regionalRg
	az deployment group what-if \
		--name "region" \
		--resource-group $(REGIONAL_RESOURCEGROUP) \
		--template-file templates/region.bicep \
		--parameters \
			configurations/region.bicepparam
.PHONY: region

#
# Service Cluster
#

svcRg:
	@if [ "$$(az group exists --name $(SVC_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(SVC_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: svcRg

svc: svcRg cleanup-orphaned-rolebindings region
	az deployment group create \
		--name "svc" \
		--resource-group $(SVC_RESOURCEGROUP) \
		--template-file templates/svc-cluster.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/svc-cluster.bicepparam
.PHONY: svc

svc.enable-aks-metrics: metrics-infra
	az aks update --enable-azure-monitor-metrics \
		--resource-group $(SVC_RESOURCEGROUP) \
		--name ${AKS_NAME} \
		--azure-monitor-workspace-resource-id $(shell az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.monitorId.value) \
		--grafana-resource-id $(shell az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.grafanaId.value)
.PHONY: svc.enable-aks-metrics

svc.aks.admin-access:
	@scripts/aks-admin-access.sh $(SVC_RESOURCEGROUP)
.PHONY: svc.aks.admin-access

svc.aks.kubeconfig:
	@az aks get-credentials --overwrite-existing --only-show-errors -n ${AKS_NAME} -g $(SVC_RESOURCEGROUP) -f "${HOME}/.kube/${SVC_RESOURCEGROUP}.kubeconfig"
	@kubelogin convert-kubeconfig -l azurecli --kubeconfig "${HOME}/.kube/${SVC_RESOURCEGROUP}.kubeconfig"
	@echo ${HOME}/.kube/${SVC_RESOURCEGROUP}.kubeconfig
.PHONY: svc.aks.kubeconfig

svc.init: svc svc.aks.admin-access svc.aks.kubeconfig svc.enable-aks-metrics
.PHONY: svc.init

svc.what-if: svcRg
	az deployment group what-if \
		--name "svc" \
		--resource-group $(SVC_RESOURCEGROUP) \
		--template-file templates/svc-cluster.bicep \
		--parameters \
			configurations/svc-cluster.bicepparam
.PHONY: svc.what-if

svc.clean:
	az group delete -g $(SVC_RESOURCEGROUP)
	az keyvault purge --name "$$(az keyvault list-deleted --query "[?properties.tags.resourceGroup == '$(SVC_RESOURCEGROUP)'].name" -ojson | jq -r '.[0]')"
.PHONY: svc.clean

#
# Management Cluster
#

mgmtRg:
	@if [ "$$(az group exists --name $(MGMT_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(MGMT_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: mgmtRg

mgmt: mgmtRg cleanup-orphaned-rolebindings region
	az deployment group create \
		--name "mgmt" \
		--resource-group $(MGMT_RESOURCEGROUP) \
		--template-file templates/mgmt-cluster.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/mgmt-cluster.bicepparam
.PHONY: mgmt

mgmt.enable-aks-metrics: metrics-infra
	az aks update --enable-azure-monitor-metrics \
		--resource-group $(MGMT_RESOURCEGROUP) \
		--name ${AKS_NAME} \
		--azure-monitor-workspace-resource-id $(shell az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.monitorId.value) \
		--grafana-resource-id $(shell az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.grafanaId.value)
.PHONY: mgmt.enable-aks-metrics

mgmt.aks.admin-access:
	@scripts/aks-admin-access.sh $(MGMT_RESOURCEGROUP)
.PHONY: mgmt.aks.admin-access

mgmt.aks.kubeconfig:
	@az aks get-credentials --overwrite-existing --only-show-errors -n ${AKS_NAME} -g $(MGMT_RESOURCEGROUP) -f "${HOME}/.kube/${MGMT_RESOURCEGROUP}.kubeconfig"
	@kubelogin convert-kubeconfig -l azurecli --kubeconfig "${HOME}/.kube/${MGMT_RESOURCEGROUP}.kubeconfig"
	@echo ${HOME}/.kube/${MGMT_RESOURCEGROUP}.kubeconfig
.PHONY: mgmt.aks.kubeconfig

mgmt.init: mgmt mgmt.aks.admin-access mgmt.aks.kubeconfig mgmt.enable-aks-metrics
.PHONY: mgmt.init

mgmt.what-if: mgmtRg
	az deployment group what-if \
		--name "mgmt" \
		--resource-group $(MGMT_RESOURCEGROUP) \
		--template-file templates/mgmt-cluster.bicep \
		--parameters \
			configurations/mgmt-cluster.bicepparam
.PHONY: mgmt.what-if

mgmt.clean:
	az group delete -g $(MGMT_RESOURCEGROUP)
	az keyvault purge --name "$$(az keyvault list-deleted --query "[?properties.tags.resourceGroup == '$(MGMT_RESOURCEGROUP)'].name" -ojson | jq -r '.[0]')"
.PHONY: mgmt.clean

# ACR

globalRg:
	@if [ "$$(az group exists --name $(GLOBAL_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(GLOBAL_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: globalRg

acr: globalRg
	az deployment group create \
		--name "acr" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/acr.bicepparam
.PHONY: acr

acr.what-if: globalRg
	az deployment group what-if \
		--name "acr" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		--parameters \
			configurations/acr.bicepparam

acr-svc: globalRg
	az deployment group create \
		--name "acr-svc" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/acr-svc.bicepparam
.PHONY: acr-svc

acr-svc.what-if: globalRg
	az deployment group what-if \
		--name "acr-svc" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		--parameters \
			configurations/acr-svc.bicepparam

acr-ocp: globalRg
	az deployment group create \
		--name "acr-ocp" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/acr-ocp.bicepparam
.PHONY: acr-ocp

acr-ocp.what-if: globalRg
	az deployment group what-if \
		--name "acr-ocp" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		--parameters \
			configurations/acr-ocp.bicepparam

#
# Postgres Authentication Helpers
#

cs-current-user-pg-connect: rg
	@scripts/cs-current-user-pg-connect.sh $(SVC_RESOURCEGROUP) $(CS_PG_NAME)
.PHONY: cs-current-user-pg-connect

cs-miwi-pg-connect: rg
	@scripts/cs-miwi-pg-connect.sh $(SVC_RESOURCEGROUP) $(CS_PG_NAME) "clusters-service" "cluster-service" "clusters-service"
.PHONY: cs-miwi-pg-connect

maestro-current-user-pg-connect: rg
	@scripts/cs-current-user-pg-connect.sh $(SVC_RESOURCEGROUP) $(MAESTRO_PG_NAME)
.PHONY: maestro-current-user-pg-connect

maestro-miwi-pg-connect: rg
	@scripts/cs-miwi-pg-connect.sh $(SVC_RESOURCEGROUP) $(MAESTRO_PG_NAME) "maestro-server" "maestro" "maestro"
.PHONY: maestro-miwi-pg-connect

#
# Metrics
#

metrics-infra: regionalRg
	 az deployment group create \
		--name "metrics-infra" \
		--resource-group $(REGIONAL_RESOURCEGROUP) \
		--template-file modules/metrics/metrics.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters configurations/metrics.bicepparam
.PHONY: metrics-infra

metrics-infra.what-if: regionalRg
	az deployment group what-if \
		--name "metrics-infra" \
		--resource-group $(REGIONAL_RESOURCEGROUP) \
		--template-file modules/metrics/metrics.bicep \
		--parameters configurations/metrics.bicepparam
.PHONY: metrics-infra.what-if

#
# Secrets
#

secrets-download:
	@[ "${SECRET_SA_ACCOUNT_NAME}" ] || ( echo ">> SECRET_SA_ACCOUNT_NAME is not set"; exit 1 )
	rm -rf secrets
	az storage blob download -n secrets.tar.gz -c secrets -f secrets.tar.gz --account-name ${SECRET_SA_ACCOUNT_NAME} --auth-mode login >/dev/null
	tar -xzf secrets.tar.gz
	rm secrets.tar.gz
.PHONY: secrets-download

secrets-yes-i-want-to-update-the-shared-secrets:
	@[ "${SECRET_SA_ACCOUNT_NAME}" ] || ( echo ">> SECRET_SA_ACCOUNT_NAME is not set"; exit 1 )
	tar -czf secrets.tar.gz secrets
	az storage blob upload -n secrets.tar.gz -c secrets -f secrets.tar.gz --overwrite --auth-mode login --account-name ${SECRET_SA_ACCOUNT_NAME} >/dev/null
	rm secrets.tar.gz
.PHONY: secrets-yes-i-want-to-update-the-shared-secrets
