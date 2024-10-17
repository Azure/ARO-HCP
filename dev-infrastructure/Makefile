SHELL = /bin/bash

CONFIG_PROFILE ?= dev
include configurations/$(CONFIG_PROFILE).mk

CURRENTUSER = $(shell az ad signed-in-user show | jq -r '.id')
DEPLOYMENTNAME = $(RESOURCEGROUP)
DEPLOYMENTNAMEREGION = $(REGIONAL_RESOURCEGROUP)-REGION

# Set SKIP_CONFIRM to a non-empty value to skip "what-if" confirmation prompts.
ifndef SKIP_CONFIRM
PROMPT_TO_CONFIRM = "--confirm-with-what-if"
endif

list:
	@grep '^[^#[:space:]].*:' Makefile
.PHONY: list

fmt:
	set -e; \
	FILES="$$(find . -type f -name "*.bicep*")"; \
	for file in $$FILES; do \
	echo "az bicep format --file $${file}"; \
	az bicep format --file $$file; \
	done
.PHONY: fmt

lint:
	set -e; \
	FILES="$$(find . -type f -name "*.bicep*")"; \
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

rg:
	@if [ "$$(az group exists --name $(RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: rg

regionalRg:
	@if [ "$$(az group exists --name $(REGIONAL_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(REGIONAL_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: regionalRg


imagesyncRg:
	@if [ "$$(az group exists --name $(IMAGE_SYNC_RESOURCEGROUP))" = "false" ]; then \
		az group create \
			--name $(IMAGE_SYNC_RESOURCEGROUP)  \
			--location $(REGION) \
			--output none; \
	fi
.PHONY: imagesyncRg

cleanup-orphaned-rolebindings:
	@scripts/cleanup-orphaned-rolebindings.sh $(RESOURCEGROUP)
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

image-sync: imagesyncRg
	az deployment group create \
		--name "image-sync" \
		--resource-group $(IMAGE_SYNC_RESOURCEGROUP) \
		--template-file templates/image-sync.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/mvp-image-sync.bicepparam

region: regionalRg
	az deployment group create \
		--name "$(DEPLOYMENTNAMEREGION)" \
		--resource-group $(REGIONAL_RESOURCEGROUP) \
		--template-file templates/region.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/region.bicepparam \
		--parameters \
			currentUserId=$(CURRENTUSER)
.PHONY: region

cluster: rg cleanup-orphaned-rolebindings region
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	az deployment group create \
		--name "$(DEPLOYMENTNAME)" \
		--resource-group $(RESOURCEGROUP) \
		--template-file templates/$(AKSCONFIG).bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/$(AKSCONFIG).bicepparam \
		--parameters \
			currentUserId=$(CURRENTUSER) \
			regionalResourceGroup=$(REGIONAL_RESOURCEGROUP)
.PHONY: cluster

cluster-what-if: rg
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	az deployment group what-if \
		--name "$(DEPLOYMENTNAME)" \
		--resource-group $(RESOURCEGROUP) \
		--template-file templates/$(AKSCONFIG).bicep \
		--parameters \
			configurations/$(AKSCONFIG).bicepparam \
		--parameters \
			currentUserId=$(CURRENTUSER) \
.PHONY: cluster-what-if

aks.admin-access: rg
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	@scripts/aks-admin-access.sh $(RESOURCEGROUP)
.PHONY: aks.admin-access

cs-current-user-pg-connect: rg
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	@scripts/cs-current-user-pg-connect.sh $(RESOURCEGROUP) "cs-pg-"
.PHONY: cs-current-user-pg-connect

cs-miwi-pg-connect: rg
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	@scripts/cs-miwi-pg-connect.sh $(RESOURCEGROUP) "cs-pg-" "clusters-service" "cluster-service" "clusters-service"
.PHONY: cs-miwi-pg-connect

maestro-current-user-pg-connect: rg
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	@scripts/cs-current-user-pg-connect.sh $(RESOURCEGROUP) "maestro-pg-"
.PHONY: maestro-current-user-pg-connect

maestro-miwi-pg-connect: rg
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	@scripts/cs-miwi-pg-connect.sh $(RESOURCEGROUP) "maestro-pg-" "maestro-server" "maestro" "maestro"
.PHONY: maestro-miwi-pg-connect

metrics-infra: regionalRg
	 CURRENTUSER=$(CURRENTUSER) az deployment group create \
		--name "metrics-infra" \
		--resource-group $(REGIONAL_RESOURCEGROUP) \
		--template-file modules/metrics/metrics.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
		  globalResourceGroup=$(REGIONAL_RESOURCEGROUP) \
		--parameters configurations/dev-metrics.bicepparam
.PHONY: metrics-infra 

enable-aks-metrics: metrics-infra
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	az aks update --enable-azure-monitor-metrics \
	--resource-group $(RESOURCEGROUP) \
	--name aro-hcp-$(AKSCONFIG) \
	--azure-monitor-workspace-resource-id $(shell az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.monitorId.value) \
	--grafana-resource-id $(shell az deployment group show --resource-group $(REGIONAL_RESOURCEGROUP) --name metrics-infra --output tsv --query properties.outputs.grafanaId.value)
.PHONY: enable-aks-metrics

aks.kubeconfig:
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	AKS_NAME="$$(az aks list --query "[?tags.clusterType == '$(AKSCONFIG)'].name" -ojson -g $(RESOURCEGROUP) | jq -r '.[0]')"; \
	az aks get-credentials -n "$${AKS_NAME}" -g $(RESOURCEGROUP) -f "${HOME}/.kube/$(AKSCONFIG).kubeconfig"
	kubelogin convert-kubeconfig -l azurecli --kubeconfig "${HOME}/.kube/$(AKSCONFIG).kubeconfig"
.PHONY: aks.kubeconfig

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

clean:
	az group delete -g $(RESOURCEGROUP)
	az keyvault purge --name "$$(az keyvault list-deleted --query "[?properties.tags.resourceGroup == '$(RESOURCEGROUP)'].name" -ojson | jq -r '.[0]')"
.PHONY: clean
