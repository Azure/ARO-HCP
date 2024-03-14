
SHELL = /bin/bash

HCPDEVSUBSCRIPTION="ARO Hosted Control Planes (EA Subscription 1)"
HCPDEVSUBSCRIPTIONID=1d3378d3-5a3f-4712-85a1-2485495dfc4b

AKSTEMPLATE=templates/aks-development.bicep
VPNTEMPLATE=templates/vpn-development.bicep
VPNCA=$(shell base64 -w0 < secrets/vpn-ca.crt)
DEPLOYMENTNAME=hcp-$(USER)-aks-cluster
	

ifdef AKSCONFIG
include ./configurations/$(AKSCONFIG)
else 
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

vpnrg: setsubscription 
	az group create \
  		--name $(VPNRESOURCEGROUP)  \
  		--location $(LOCATION) \
		--tags "CreatedByConfig=${AKSCONFIG}"

vpn.localconfig:
	( RESOURCEGROUP=$(VPNRESOURCEGROUP) LOCATION=$(LOCATION) scripts/vpn-config.sh vpn-$(VPNRESOURCEGROUP).ovpn ) 
	
vpngw: setsubscription vpnrg
	az deployment group create \
        --resource-group $(VPNRESOURCEGROUP)\
        --name "$(DEPLOYMENTNAME)-vpn" \
        --template-file $(VPNTEMPLATE) \
        --parameters \
             vpnCACertificate=$(VPNCA) 
		
aks.linked: setsubscription rg
	az deployment group create \
  		--name "$(DEPLOYMENTNAME)-aks" \
  		--resource-group $(RESOURCEGROUP) \
  		--template-file $(AKSTEMPLATE) \
		--parameters \
			kubernetesVersion=$(AKSVERSION) \
		    vnetName=$(AKSVNETNAME) \
			vnetAddressPrefix=$(VNETCIDR) \
			subnetPrefix=$(NODESUBNETCIDR) \
			podSubnetPrefix=$(PODSUBNETCIDR) \
			vpnrg=$(VPNRESOURCEGROUP) \
			createdByConfigTag=${AKSCONFIG}


aks.standalone: setsubscription rg vpngw aks.linked
	echo "Installation of a standalone AKS Cluster behind a VPN Gateway"
		
aks.kubeconfig:
	az aks get-credentials -n aro-hcp-cluster-001 -g $(RESOURCEGROUP) --public-fqdn -a -f aks.kubeconfig

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
	scripts/removeDanglingPeerings.sh $(VPNRESOURCEGROUP) 

.PHONY: aks.kubeconfig
	
