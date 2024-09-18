SHELL = /bin/bash
AKSCONFIG ?= svc-cluster

CONFIG_PROFILE ?= dev
include ../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk

CONSUMER_NAME ?= $(shell az aks list --query "[?tags.clusterType == 'mgmt-cluster' && starts_with(resourceGroup, '$(REGIONAL_RESOURCEGROUP)')].resourceGroup" -o tsv)
KEYVAULT_NAME ?= $(shell az keyvault list --query "[?starts_with(name, 'service-kv')].name" -g ${RESOURCEGROUP} --output tsv)
FPA_CERT_NAME ?= firstPartyMock

deploy:
	ZONE_RESOURCE_ID=$(shell az network dns zone list -g ${REGIONAL_RESOURCEGROUP} --query "[?zoneType=='Public'].id" -o tsv) && \
	sed -e "s#ZONE_RESOURCE_ID#$${ZONE_RESOURCE_ID}#g" -e "s/REGION/${REGION}/g" -e "s/CONSUMER_NAME/${CONSUMER_NAME}/g" deploy/mvp-provisioning-shards.yml > deploy/tmp-provisioning-shard.yml
	oc process --local -f deploy/openshift-templates/arohcp-namespace-template.yml \
	  -p ISTIO_VERSION=asm-1-21 | oc apply -f -
	kubectl apply -f deploy/istio.yml
	oc process --local -f deploy/openshift-templates/arohcp-db-template.yml | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml \
	  -p PROVISION_SHARDS_CONFIG="$$( base64 -i deploy/tmp-provisioning-shard.yml)" | oc apply -f -
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n clusters-service \
			--query clientId) && \
	AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID=$(shell az ad app list \
			--display-name aro-hcp-dev-first-party \
			--query '[*]'.appId \
			-o tsv) && \
	CS_SERVICE_PRINCIPAL_CREDS_BASE64='$(shell az keyvault secret show --vault-name "service-kv-aro-hcp-dev" --name "aro-hcp-dev-sp-cs" | jq .value -r | base64 | tr -d '\n')' && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	oc process --local -f deploy/openshift-templates/arohcp-service-template.yml \
	  -p AZURE_CS_MI_CLIENT_ID=$${AZURE_CS_MI_CLIENT_ID} \
	  -p TENANT_ID=$${TENANT_ID} \
	  -p REGION=${REGION} \
	  -p SERVICE_KEYVAULT_NAME=${KEYVAULT_NAME} \
	  -p CS_SERVICE_PRINCIPAL_CREDS_BASE64=$${CS_SERVICE_PRINCIPAL_CREDS_BASE64} \
	  -p IMAGE_REGISTRY=${ARO_HCP_IMAGE_ACR}.azurecr.io \
	  -p IMAGE_REPOSITORY=app-sre/uhc-clusters-service \
	  -p AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID=$${AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID} \
	  -p FPA_CERT_NAME=${FPA_CERT_NAME} \
	  -p IMAGE_TAG=3482df9 | oc apply -f -

deploy-integ:
	oc process --local -f deploy/integration/cluster-service-namespace.yaml | oc apply -f -

# for local development
provision-shard:
	@ZONE_RESOURCE_ID=$(shell az network dns zone list -g ${REGIONAL_RESOURCEGROUP} --query "[?zoneType=='Public'].id" -o tsv) && \
	sed -e "s#ZONE_RESOURCE_ID#$${ZONE_RESOURCE_ID}#g" -e "s/REGION/${REGION}/g" -e "s/CONSUMER_NAME/${CONSUMER_NAME}/g" deploy/dev-provisioning-shards.yml

.PHONY: deploy deploy-integ provision-shard

copy-fpc:
	@FPC_SECRET=$(shell az keyvault secret show --vault-name "service-kv-aro-hcp-dev" --name ${FPA_CERT_NAME} --query "value" -o tsv) && \
 	../dev-infrastructure/scripts/kv-add-secret.sh ${KEYVAULT_NAME} ${RESOURCEGROUP} ${FPA_CERT_NAME} $${FPC_SECRET}
.PHONY: copy-fpc
