
SHELL = /bin/bash

HCPDEVSUBSCRIPTION="ARO Hosted Control Planes (EA Subscription 1)"
HCPDEVSUBSCRIPTIONID=1d3378d3-5a3f-4712-85a1-2485495dfc4b

AKSTEMPLATE=templates/aks-development.bicep
CURRENTUSER=$(shell az ad signed-in-user show | jq -r '.id')
DEPLOYMENTNAME=hcp-$(USER)
LOCATION?=eastus
RESOURCEGROUP=aro-hcp-${AKSCONFIG}-$(USER)

ifndef AKSCONFIG
$(error "Must set AKSCONFIG")
endif


list:
	@grep '^[^#[:space:]].*:' Makefile

show:
	@echo "Resource group      : $(RESOURCEGROUP)"
	@echo "VPN resource group  : $(VPNRESOURCEGROUP)"
	@echo "AddressSpace prefix : $(PREFIX)"
	@echo "AKS version         : $(AKSVERSION)"
	@echo "Location            : $(LOCATION)"
	@echo "AKS VNet Name       : $(AKSVNETNAME)"
	@echo "DNSZone/deployment  : $(DEPLOYMENTNAME)"

setsubscription:
	az account set --subscription $(HCPDEVSUBSCRIPTION)
	az account show

rg: setsubscription
	az group create \
  		--name $(RESOURCEGROUP)  \
  		--location $(LOCATION) \
		--tags "CreatedByConfig=${AKSCONFIG}"

dev.infrastructure: setsubscription rg
	az deployment group create \
		--name "$(DEPLOYMENTNAME)-dev-infra" \
		--resource-group $(RESOURCEGROUP) \
		--template-file $(AKSTEMPLATE) \
		--confirm-with-what-if \
		--parameters \
			configurations/${AKSCONFIG}.bicepparam \
		--parameters \
			currentUserId=$(CURRENTUSER)

aks.kubeconfig:
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

.PHONY: aks.kubeconfig
