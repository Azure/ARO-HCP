SHELL = /bin/bash

CURRENTUSER=$(shell az ad signed-in-user show | jq -r '.id')
LOCATION?=westus3
RESOURCEGROUP?=aro-hcp-${AKSCONFIG}-$(USER)
MAESTRO_INFRA_RESOURCEGROUP?=aro-hcp-svc-cluster-$(USER)
DNS_RESOURCEGROUP?=aro-hcp-svc-cluster-$(USER)
DEPLOYMENTNAME=$(RESOURCEGROUP)
DEPLOYMENTNAMEACR=$(RESOURCEGROUP)-ACR
ACRNAME?=devarochp$(shell echo $(CURRENTUSER) | sha256sum  | head -c 24) 

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

rg:
	@az group create \
  		--name $(RESOURCEGROUP)  \
  		--location $(LOCATION) \
		--output none
.PHONY: rg

cleanup-orphaned-rolebindings:
	@scripts/cleanup-orphaned-rolebindings.sh $(RESOURCEGROUP)
	@scripts/cleanup-orphaned-rolebindings.sh aro-hcp-dev
.PHONY: cleanup-orphaned-rolebindings

acr: rg
	az deployment group create \
		--name "$(DEPLOYMENTNAMEACR)" \
		--resource-group $(RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		--confirm-with-what-if \
		--parameters \
			configurations/dev-acr.bicepparam \
		--parameters \
			acrName=$(ACRNAME)
.PHONY: acr

cluster: rg cleanup-orphaned-rolebindings
ifndef AKSCONFIG
	$(error "Must set AKSCONFIG")
endif
	az deployment group create \
		--name "$(DEPLOYMENTNAME)" \
		--resource-group $(RESOURCEGROUP) \
		--template-file templates/$(AKSCONFIG).bicep \
		--confirm-with-what-if \
		--parameters \
			configurations/$(AKSCONFIG).bicepparam \
		--parameters \
			currentUserId=$(CURRENTUSER) \
			maestroInfraResourceGroup=$(MAESTRO_INFRA_RESOURCEGROUP)
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
			maestroInfraResourceGroup=$(MAESTRO_INFRA_RESOURCEGROUP)
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
