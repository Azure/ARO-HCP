SHELL = /bin/bash

CURRENTUSER=$(shell az ad signed-in-user show | jq -r '.id')
LOCATION?=eastus
RESOURCEGROUP=aro-hcp-${AKSCONFIG}-$(USER)
DEPLOYMENTNAME=$(RESOURCEGROUP)

list:
	@grep '^[^#[:space:]].*:' Makefile

fmt:
	set -e; \
	FILES="$$(find . -type f -name "*.bicep*")"; \
	for file in $$FILES; do \
	echo "az bicep format --file $${file}"; \
	az bicep format --file $$file; \
	done

lint:
	set -e; \
	FILES="$$(find . -type f -name "*.bicep*")"; \
	for file in $$FILES; do \
	echo "az bicep lint --file $${file}"; \
	az bicep lint --file $$file; \
	done

rg: 
	az group create \
  		--name $(RESOURCEGROUP)  \
  		--location $(LOCATION)

svc-cluster: rg
	az deployment group create \
		--name "$(DEPLOYMENTNAME)" \
		--resource-group $(RESOURCEGROUP) \
		--template-file templates/svc-cluster.bicep \
		--confirm-with-what-if \
		--parameters \
			configurations/${AKSCONFIG}.bicepparam \
		--parameters \
			currentUserId=$(CURRENTUSER)

mgmt-cluster: rg
	az deployment group create \
		--name "$(DEPLOYMENTNAME)" \
		--resource-group $(RESOURCEGROUP) \
		--template-file templates/mgmt-cluster.bicep \
		--confirm-with-what-if \
		--parameters \
			configurations/${AKSCONFIG}.bicepparam \
		--parameters \
			currentUserId=$(CURRENTUSER)

aks.kubeconfig: 
	AKS_NAME="$$(az aks list --query "[?tags.clusterType == '$(AKSCONFIG)'].name" -ojson | jq -r '.[0]')"; \
	az aks get-credentials -n "$${AKS_NAME}" -g $(RESOURCEGROUP) -a -f "$(AKSCONFIG).kubeconfig"

secrets-download:
	@[ "${SECRET_SA_ACCOUNT_NAME}" ] || ( echo ">> SECRET_SA_ACCOUNT_NAME is not set"; exit 1 )
	rm -rf secrets
	az storage blob download -n secrets.tar.gz -c secrets -f secrets.tar.gz --account-name ${SECRET_SA_ACCOUNT_NAME} --auth-mode login >/dev/null
	tar -xzf secrets.tar.gz
	rm secrets.tar.gz

secrets-yes-i-want-to-update-the-shared-secrets: 
	@[ "${SECRET_SA_ACCOUNT_NAME}" ] || ( echo ">> SECRET_SA_ACCOUNT_NAME is not set"; exit 1 )
	tar -czf secrets.tar.gz secrets
	az storage blob upload -n secrets.tar.gz -c secrets -f secrets.tar.gz --overwrite --auth-mode login --account-name ${SECRET_SA_ACCOUNT_NAME} >/dev/null
	rm secrets.tar.gz

clean:
	az group delete -g $(RESOURCEGROUP)
	az keyvault purge --name "$$(az keyvault list-deleted --query "[?properties.tags.resourceGroup == '$(RESOURCEGROUP)'].name" -ojson | jq -r '.[0]')"

.PHONY: aks.kubeconfig
