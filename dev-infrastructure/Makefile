SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
PRINCIPAL_ID ?= $(shell az ad signed-in-user show -o json | jq -r '.id')
$(shell ./create-config.sh $(DEPLOY_ENV))
include config.mk

# Set SKIP_CONFIRM to a non-empty value to skip "what-if" confirmation prompts.
ifndef SKIP_CONFIRM
PROMPT_TO_CONFIRM = "--confirm-with-what-if"
endif

PERSIST ?= false

MGMT_KUBECONFIG_FILE ?= ${HOME}/.kube/${MGMT_RESOURCEGROUP}.kubeconfig
SVC_KUBECONFIG_FILE ?= ${HOME}/.kube/${SVC_RESOURCEGROUP}.kubeconfig

DEPLOYMENT_NAME_SUFFIX ?=
GLOBAL_RG_DEPLOYMENT_NAME ?= global${DEPLOYMENT_NAME_SUFFIX}
MGMG_RG_DEPLOYMENT_NAME ?= mgmt${DEPLOYMENT_NAME_SUFFIX}
SVC_RG_DEPLOYMENT_NAME ?= svc${DEPLOYMENT_NAME_SUFFIX}
IMAGE_SYNC_RG_DEPLOYMENT_NAME ?= image-sync${DEPLOYMENT_NAME_SUFFIX}
REGIONAL_RG_DEPLOYMENT_NAME ?= region${DEPLOYMENT_NAME_SUFFIX}
METRICS_INFRA_RG_DEPLOYMENT_NAME ?= metrics-infra${DEPLOYMENT_NAME_SUFFIX}
ROLE_ASSIGNMENTS_DEPLOYMENT_NAME ?= role-assignments${DEPLOYMENT_NAME_SUFFIX}

list:
	@grep '^[^#[:space:]].*:' Makefile
.PHONY: list

fmt:
	set -e; \
	FILES="$$(find . -type f -name "*.bicep*" ! -name "*.tmpl.bicepparam")"; \
	for file in $$FILES; do \
	echo "az bicep format --file $${file}"; \
	az bicep format --file $$file; \
	done
.PHONY: fmt

lint:
	set -e; \
	FILES="$$(find . -type f -name "*.bicep*" ! -name "*.tmpl.bicepparam")"; \
	for file in $$FILES; do \
	echo "az bicep lint --file $${file}"; \
	az bicep lint --file $$file; \
	done
.PHONY: lint

feature-registration: # hardcoded to eastus as this is a subscription deployment, not a resource group
	@az deployment sub create \
		--name "feature-registration" \
		--location eastus \
		--template-file templates/feature-registration.bicep \
		$(PROMPT_TO_CONFIRM)
.PHONY: feature-registration

cleanup-orphaned-rolebindings:
	@scripts/cleanup-orphaned-rolebindings.sh $(SVC_RESOURCEGROUP)
	@scripts/cleanup-orphaned-rolebindings.sh $(MGMT_RESOURCEGROUP)
	@scripts/cleanup-orphaned-rolebindings.sh $(REGIONAL_RESOURCEGROUP)
	@scripts/cleanup-orphaned-rolebindings.sh ${GLOBAL_RESOURCEGROUP}
.PHONY: cleanup-orphaned-rolebindings

create-mock-identities:
	az deployment group wait --created --name "aro-hcp-dev-mock-certificates" --resource-group $(GLOBAL_RESOURCEGROUP) --interval 10
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

imagesync.rg:
	@if [ "$$(az group exists --name $(IMAGE_SYNC_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(IMAGE_SYNC_RESOURCEGROUP)  \
			--location $(REGION) \
			--tags persist=$(PERSIST) \
			--output none; \
	fi
.PHONY: imagesyncRg

imagesync.wait:
	@./ensure-no-running-deployment.sh $(IMAGE_SYNC_RESOURCEGROUP) $(IMAGE_SYNC_RG_DEPLOYMENT_NAME)
.PHONY: imagesync.wait

imagesync: imagesync.rg imagesync.wait
	az deployment group create \
		--name $(IMAGE_SYNC_RG_DEPLOYMENT_NAME) \
		--resource-group $(IMAGE_SYNC_RESOURCEGROUP) \
		--template-file templates/image-sync.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/image-sync.bicepparam
.PHONY: imagesync

imagesync.what-if: imagesync.rg imagesync.wait
	az deployment group what-if \
		--name $(IMAGE_SYNC_RG_DEPLOYMENT_NAME) \
		--resource-group $(IMAGE_SYNC_RESOURCEGROUP) \
		--template-file templates/image-sync.bicep \
		--parameters \
			configurations/image-sync.bicepparam
.PHONY: imagesync.what-if

imagesync.clean:
	@if [ "$$(az group exists --name $(IMAGE_SYNC_RESOURCEGROUP))" = "true" ]; then \
		az group delete -g $(IMAGE_SYNC_RESOURCEGROUP); \
	fi
.PHONY: imagesync.clean

#
# Region
#

regional.rg:
	@if [ "$$(az group exists --name $(REGIONAL_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(REGIONAL_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: regional.rg

region.wait:
	@./ensure-no-running-deployment.sh $(REGIONAL_RESOURCEGROUP) $(REGIONAL_RG_DEPLOYMENT_NAME)
.PHONY: region.wait

region: region.wait regional.rg
	@scripts/cleanup-orphaned-rolebindings.sh $(REGIONAL_RESOURCEGROUP)
	az deployment group create \
		--name $(REGIONAL_RG_DEPLOYMENT_NAME) \
		--resource-group $(REGIONAL_RESOURCEGROUP) \
		--template-file templates/region.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/region.bicepparam \
		--parameters \
			persist=${PERSIST}
.PHONY: region

region.clean:
	@if [ "$$(az group exists --name $(REGIONAL_RESOURCEGROUP))" = "true" ]; then \
		echo "Will delete Azure resource group $(REGIONAL_RESOURCEGROUP)"; \
		az group delete -g $(REGIONAL_RESOURCEGROUP); \
	fi
.PHONY: region.clean

region.what-if: region.wait regional.rg
	az deployment group what-if \
		--name $(REGIONAL_RG_DEPLOYMENT_NAME) \
		--resource-group $(REGIONAL_RESOURCEGROUP) \
		--template-file templates/region.bicep \
		--parameters \
			configurations/region.bicepparam
.PHONY: region.what-if

#
# Service Cluster
#

svc.rg:
	@if [ "$$(az group exists --name $(SVC_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(SVC_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: svc.rg

svc: svc.rg
	@scripts/cleanup-orphaned-rolebindings.sh $(SVC_RESOURCEGROUP)
	@./ensure-no-running-deployment.sh $(SVC_RESOURCEGROUP) $(SVC_RG_DEPLOYMENT_NAME)-infra
	az deployment group create \
		--name $(SVC_RG_DEPLOYMENT_NAME)-infra \
		--resource-group $(SVC_RESOURCEGROUP) \
		--template-file templates/svc-infra.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/svc-infra.bicepparam \
		--parameters \
			persist=${PERSIST}
	@./ensure-no-running-deployment.sh $(SVC_RESOURCEGROUP) $(SVC_RG_DEPLOYMENT_NAME)
	az deployment group create \
		--name $(SVC_RG_DEPLOYMENT_NAME) \
		--resource-group $(SVC_RESOURCEGROUP) \
		--template-file templates/svc-cluster.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/svc-cluster.bicepparam \
		--parameters \
			persist=${PERSIST}

	@scripts/aks-admin-access.sh $(SVC_RESOURCEGROUP) $(PRINCIPAL_ID)
	@scripts/aks-kubeconfig.sh $(SVC_RESOURCEGROUP) $(AKS_NAME) "$(SVC_KUBECONFIG_FILE)"
	@KUBEONFIG=$(SVC_KUBECONFIG_FILE) TARGET_VERSION=$(ISTIO_TARGET_VERSION) ISTIOCTL_VERSION=$(ISTIOCTL_VERSION) TAG=$(ISTIO_TAG) scripts/istio.sh
.PHONY: svc

svc.enable-aks-metrics:
	@if [ "$$(az aks show --resource-group $(SVC_RESOURCEGROUP) --name ${AKS_NAME} --query 'azureMonitorProfile.metrics.enabled' -o tsv)" = "true" ]; then \
        echo "Azure Monitor metrics are already enabled."; \
    else \
        az aks update --enable-azure-monitor-metrics \
            --resource-group $(SVC_RESOURCEGROUP) \
            --name ${AKS_NAME} \
            --azure-monitor-workspace-resource-id $$(az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.monitorId.value) \
            --grafana-resource-id $$(az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.grafanaId.value); \
    fi
.PHONY: svc.enable-aks-metrics

svc.cs-pr-check-msi: svc.rg
	@./ensure-no-running-deployment.sh $(SVC_RESOURCEGROUP) cs-integ-msi
	az deployment group create \
		--name cs-integ-msi \
		--resource-group $(SVC_RESOURCEGROUP) \
		--template-file templates/cs-integration-msi.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/cs-integ-msi.bicepparam
.PHONY: svc.cs-pr-check-msi

svc.aks.admin-access:
	@scripts/aks-admin-access.sh $(SVC_RESOURCEGROUP) $(PRINCIPAL_ID)
.PHONY: svc.aks.admin-access

svc.aks.kubeconfig:
	@scripts/aks-kubeconfig.sh $(SVC_RESOURCEGROUP) $(AKS_NAME) "$(SVC_KUBECONFIG_FILE)"
.PHONY: svc.aks.kubeconfig

svc.aks.kubeconfigfile:
	@echo ${SVC_KUBECONFIG_FILE}
.PHONY: svc.aks.kubeconfigfile

svc.oidc.storage.permissions:
	@STORAGEACCOUNTID=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${SVC_RESOURCEGROUP} --query id -o tsv) && \
	az role assignment create \
	--role "Storage Blob Data Contributor" \
	--assignee ${PRINCIPAL_ID} \
	--scope "$${STORAGEACCOUNTID}" \
	--only-show-errors
.PHONY: svc.oidc.storage.permissions

svc.kv.permission:
	@scripts/kv-permissions.sh $(PRINCIPAL_ID) $(SVC_KV_RESOURCEGROUP) $(SVC_KV_NAME)
.PHONY: svc.kv.permission

svc.init: region svc svc.aks.admin-access svc.aks.kubeconfig metrics-infra svc.enable-aks-metrics svc.oidc.storage.permissions
.PHONY: svc.init

svc.what-if: svc.rg
	az deployment group what-if \
		--name $(SVC_RG_DEPLOYMENT_NAME)-infra \
		--resource-group $(SVC_RESOURCEGROUP) \
		--template-file templates/svc-infra.bicep \
		--parameters \
			configurations/svc-infra.bicepparam
	az deployment group what-if \
		--name $(SVC_RG_DEPLOYMENT_NAME) \
		--resource-group $(SVC_RESOURCEGROUP) \
		--template-file templates/svc-cluster.bicep \
		--parameters \
			configurations/svc-cluster.bicepparam
.PHONY: svc.what-if

svc.dev-role-assignments:
	@./ensure-no-running-deployment.sh $(SVC_RESOURCEGROUP) ${ROLE_ASSIGNMENTS_DEPLOYMENT_NAME}
	az deployment group create \
		--name ${ROLE_ASSIGNMENTS_DEPLOYMENT_NAME} \
		--resource-group "${SVC_RESOURCEGROUP}" \
		--template-file templates/dev-roleassignments.bicep \
		--parameters configurations/dev-role-assignments.bicepparam \
		--parameters principalID=${PRINCIPAL_ID}
.PHONY: svc.dev-role-assignments

svc.clean:
	@if [ "$$(az group exists --name $(SVC_RESOURCEGROUP))" = "true" ]; then \
		echo "Will delete Azure resource group $(SVC_RESOURCEGROUP)"; \
		az group delete -g $(SVC_RESOURCEGROUP); \
	fi
.PHONY: svc.clean

#
# Management Cluster
#

mgmt.rg:
	@if [ "$$(az group exists --name $(MGMT_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(MGMT_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: mgmt.rg

mgmt.wait:
	@./ensure-no-running-deployment.sh $(MGMT_RESOURCEGROUP) $(MGMG_RG_DEPLOYMENT_NAME)
.PHONY: mgmt.wait

mgmt: mgmt.wait mgmt.rg
	@./ensure-no-running-deployment.sh $(MGMT_RESOURCEGROUP) $(MGMT_RESOURCEGROUP)-infra
	az deployment group create \
		--name $(MGMT_RESOURCEGROUP)-infra \
		--resource-group $(MGMT_RESOURCEGROUP) \
		--template-file templates/mgmt-infra.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/mgmt-infra.bicepparam \
		--parameters \
			persist=${PERSIST}
	@scripts/cleanup-orphaned-rolebindings.sh $(MGMT_RESOURCEGROUP)
	az deployment group create \
		--name $(MGMG_RG_DEPLOYMENT_NAME) \
		--resource-group $(MGMT_RESOURCEGROUP) \
		--template-file templates/mgmt-cluster.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/mgmt-cluster.bicepparam \
		--parameters \
			persist=${PERSIST}
.PHONY: mgmt

mgmt.enable-aks-metrics:
	@if [ "$$(az aks show --resource-group $(MGMT_RESOURCEGROUP) --name ${AKS_NAME} --query 'azureMonitorProfile.metrics.enabled' -o tsv)" = "true" ]; then \
        echo "Azure Monitor metrics are already enabled."; \
    else \
        az aks update --enable-azure-monitor-metrics \
            --resource-group $(MGMT_RESOURCEGROUP) \
            --name ${AKS_NAME} \
            --azure-monitor-workspace-resource-id $$(az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.monitorId.value) \
            --grafana-resource-id $$(az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.grafanaId.value); \
    fi
.PHONY: mgmt.enable-aks-metrics

mgmt.aks.admin-access:
	@scripts/aks-admin-access.sh $(MGMT_RESOURCEGROUP) $(PRINCIPAL_ID)
.PHONY: mgmt.aks.admin-access

mgmt.aks.kubeconfig: mgmt.aks.kubeconfigfile
	@scripts/aks-kubeconfig.sh $(MGMT_RESOURCEGROUP) $(AKS_NAME) "$(MGMT_KUBECONFIG_FILE)"
.PHONY: mgmt.aks.kubeconfig

mgmt.aks.kubeconfigfile:
	@echo ${MGMT_KUBECONFIG_FILE}
.PHONY: mgmt.aks.kubeconfigfile

mgmt.init: region mgmt mgmt.aks.admin-access mgmt.aks.kubeconfig metrics-infra mgmt.enable-aks-metrics
.PHONY: mgmt.init

mgmt.what-if: mgmt.rg
	az deployment group what-if \
		--name $(MGMG_RG_DEPLOYMENT_NAME)-infra \
		--resource-group $(MGMT_RESOURCEGROUP) \
		--template-file templates/mgmt-infra.bicep \
		--parameters \
			configurations/mgmt-infra.bicepparam
	az deployment group what-if \
		--name $(MGMG_RG_DEPLOYMENT_NAME) \
		--resource-group $(MGMT_RESOURCEGROUP) \
		--template-file templates/mgmt-cluster.bicep \
		--parameters \
			configurations/mgmt-cluster.bicepparam
.PHONY: mgmt.what-if

mgmt.clean:
	@if [ "$$(az group exists --name $(MGMT_RESOURCEGROUP))" = "true" ]; then \
		echo "Will delete Azure resource group $(MGMT_RESOURCEGROUP)"; \
		az group delete -g $(MGMT_RESOURCEGROUP); \
	fi
.PHONY: mgmt.clean

mgmt.kv.permission:
	@scripts/kv-permissions.sh $(PRINCIPAL_ID) $(MGMT_RESOURCEGROUP) $(CX_KV_NAME)
	@scripts/kv-permissions.sh $(PRINCIPAL_ID) $(MGMT_RESOURCEGROUP) $(MSI_KV_NAME)
	@scripts/kv-permissions.sh $(PRINCIPAL_ID) $(MGMT_RESOURCEGROUP) $(MGMT_KV_NAME)
.PHONY: mgmt.kv.permission

# ACR

global.rg:
	@if [ "$$(az group exists --name $(GLOBAL_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(GLOBAL_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: global.rg

acr: global.rg
	@./ensure-no-running-deployment.sh $(GLOBAL_RESOURCEGROUP) ${GLOBAL_RG_DEPLOYMENT_NAME}-acr
	az deployment group create \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-acr \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/global-acr.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/global-acr.bicepparam
	@./ensure-no-running-deployment.sh $(GLOBAL_RESOURCEGROUP) ${GLOBAL_RG_DEPLOYMENT_NAME}-roles
	az deployment sub create \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-roles \
		--template-file templates/global-roles.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/global-roles.bicepparam \
		--location ${GLOBAL_REGION}
.PHONY: acr

acr.what-if: global.rg
	az deployment group what-if \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-acr \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/global-acr.bicep \
		--parameters \
			configurations/global-acr.bicepparam
	az deployment sub what-if \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-roles \
		--template-file templates/global-roles.bicep \
		--parameters \
			configurations/global-roles.bicepparam \
		--location ${GLOBAL_REGION}
.PHONY: acr.what-if

acr-svc-cfg: acr
	@./ensure-no-running-deployment.sh $(GLOBAL_RESOURCEGROUP) ${GLOBAL_RG_DEPLOYMENT_NAME}-acr-svc
	az deployment group create \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-acr-svc \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/acr-svc.bicepparam
.PHONY: acr-svc-cfg

acr-svc-cfg.what-if: global.rg
	az deployment group what-if \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-acr-svc \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		--parameters \
			configurations/acr-svc.bicepparam
.PHONY: acr-svc-cfg.what-if

acr-ocp-cfg: acr
	@./ensure-no-running-deployment.sh $(GLOBAL_RESOURCEGROUP) ${GLOBAL_RG_DEPLOYMENT_NAME}-acr-ocp
	az deployment group create \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-acr-ocp \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/acr-ocp.bicepparam
.PHONY: acr-ocp-cfg

acr-ocp-cfg.what-if: global.rg
	az deployment group what-if \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-acr-ocp \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		--parameters \
			configurations/acr-ocp.bicepparam
.PHONY: acr-ocp-cfg.what-if

#
# Postgres Authentication Helpers
#

cs-current-user-pg-connect:
	@scripts/cs-current-user-pg-connect.sh $(SVC_RESOURCEGROUP) $(CS_PG_NAME)
.PHONY: cs-current-user-pg-connect

cs-miwi-pg-connect:
	@scripts/cs-miwi-pg-connect.sh $(SVC_RESOURCEGROUP) $(CS_PG_NAME) "clusters-service" "cluster-service" "clusters-service"
.PHONY: cs-miwi-pg-connect

maestro-current-user-pg-connect:
	@scripts/cs-current-user-pg-connect.sh $(SVC_RESOURCEGROUP) $(MAESTRO_PG_NAME)
.PHONY: maestro-current-user-pg-connect

maestro-miwi-pg-connect:
	@scripts/cs-miwi-pg-connect.sh $(SVC_RESOURCEGROUP) $(MAESTRO_PG_NAME) "maestro-server" "maestro" "maestro"
.PHONY: maestro-miwi-pg-connect

#
# Metrics
#

metrics-infra: regional.rg
	@./ensure-no-running-deployment.sh $(REGIONAL_RESOURCEGROUP) $(METRICS_INFRA_RG_DEPLOYMENT_NAME)
	az deployment group create \
		--name $(METRICS_INFRA_RG_DEPLOYMENT_NAME) \
		--resource-group $(REGIONAL_RESOURCEGROUP) \
		--template-file modules/metrics/metrics.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters configurations/metrics.bicepparam
.PHONY: metrics-infra

metrics-infra.what-if: regional.rg
	az deployment group what-if \
		--name $(METRICS_INFRA_RG_DEPLOYMENT_NAME) \
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

#
# Operator roles for platform workload identity
#

operator-roles:
	az deployment sub create \
	    --location westus3 \
		--name operator-roles \
		--template-file templates/dev-operator-roles.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters configurations/dev-operator-roles.bicepparam
.PHONY: operator-roles

operator-roles.what-if:
	az deployment sub what-if \
	    --location westus3 \
		--name operator-roles \
		--template-file templates/dev-operator-roles.bicep \
		--parameters configurations/dev-operator-roles.bicepparam
.PHONY: operator-roles.what-if

#
# Common
#

what-if: acr.what-if acr-svc-cfg.what-if acr-ocp-cfg.what-if region.what-if svc.what-if mgmt.what-if metrics-infra.what-if imagesync.what-if operator-roles.what-if
.PHONY: what-if

infra: region svc.init mgmt.init
.PHONY: infra

clean: svc.clean mgmt.clean region.clean
.PHONY: clean

#
# Local CS Development
#

local-cs-permissions: svc.oidc.storage.permissions svc.kv.permission mgmt.kv.permission
.PHONY: local-cs-permissions
