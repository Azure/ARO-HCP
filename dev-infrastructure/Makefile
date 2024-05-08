SHELL = /bin/bash

HCPDEVSUBSCRIPTION="ARO Hosted Control Planes (EA Subscription 1)"
HCPDEVSUBSCRIPTIONID=1d3378d3-5a3f-4712-85a1-2485495dfc4b

CURRENTUSER=$(shell az ad signed-in-user show | jq -r '.id')
DEPLOYMENTNAME=hcp-$(USER)-dev-infra
LOCATION?=eastus
RESOURCEGROUP=aro-hcp-${AKSCONFIG}-$(USER)

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

setsubscription:
	if [[ -z $$AKSCONFIG ]]; then \
		echo "Must set AKSCONFIG"; \
		exit 1; \
	fi
	az account set --subscription $(HCPDEVSUBSCRIPTION)
	az account show

rg: setsubscription
	az group create \
  		--name $(RESOURCEGROUP)  \
  		--location $(LOCATION)

svc-cluster: setsubscription rg
	az deployment group create \
		--name "$(DEPLOYMENTNAME)" \
		--resource-group $(RESOURCEGROUP) \
		--template-file templates/svc-cluster.bicep \
		--confirm-with-what-if \
		--parameters \
			configurations/${AKSCONFIG}.bicepparam \
		--parameters \
			currentUserId=$(CURRENTUSER)

mgmt-cluster: setsubscription rg
	az deployment group create \
		--name "$(DEPLOYMENTNAME)" \
		--resource-group $(RESOURCEGROUP) \
		--template-file templates/mgmt-cluster.bicep \
		--confirm-with-what-if \
		--parameters \
			configurations/${AKSCONFIG}.bicepparam \
		--parameters \
			currentUserId=$(CURRENTUSER)

aks.kubeconfig: setsubscription
	az aks get-credentials -n aro-hcp-cluster-001 -g $(RESOURCEGROUP) -a -f aks.kubeconfig

secrets-download: setsubscription
	@[ "${SECRET_SA_ACCOUNT_NAME}" ] || ( echo ">> SECRET_SA_ACCOUNT_NAME is not set"; exit 1 )
	rm -rf secrets
	az storage blob download -n secrets.tar.gz -c secrets -f secrets.tar.gz --account-name ${SECRET_SA_ACCOUNT_NAME} --auth-mode login >/dev/null
	tar -xzf secrets.tar.gz
	rm secrets.tar.gz

secrets-yes-i-want-to-update-the-shared-secrets: setsubscription
	@[ "${SECRET_SA_ACCOUNT_NAME}" ] || ( echo ">> SECRET_SA_ACCOUNT_NAME is not set"; exit 1 )
	tar -czf secrets.tar.gz secrets
	az storage blob upload -n secrets.tar.gz -c secrets -f secrets.tar.gz --overwrite --auth-mode login --account-name ${SECRET_SA_ACCOUNT_NAME} >/dev/null
	rm secrets.tar.gz

clean: setsubscription
	az group delete -g $(RESOURCEGROUP)
	az keyvault purge --name "$$(az keyvault list-deleted --query "[?properties.tags.resourceGroup == '$(RESOURCEGROUP)'].name" -ojson | jq -r '.[0]')"

.PHONY: aks.kubeconfig
