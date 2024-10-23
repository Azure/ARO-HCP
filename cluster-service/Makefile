SHELL = /bin/bash
AKSCONFIG ?= svc-cluster

CONFIG_PROFILE ?= dev
include ../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk

CONSUMER_NAME ?= $(shell az aks list --query "[?tags.clusterType == 'mgmt-cluster' && starts_with(resourceGroup, '$(REGIONAL_RESOURCEGROUP)')].resourceGroup" -o tsv)
KEYVAULT_NAME ?= $(shell az keyvault list --query "[?tags.aroHCPPurpose=='service'].name" -g  ${SVC_KV_RESOURCEGROUP} --output tsv)
FPA_CERT_NAME ?= firstPartyCert
AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID ?= "57e54810-3138-4f38-bd3b-29cb33f4c358"

deploy:
	ZONE_RESOURCE_ID=$(shell az network dns zone list -g ${REGIONAL_RESOURCEGROUP} --query "[?zoneType=='Public'].id" -o tsv) && \
	sed -e "s#ZONE_RESOURCE_ID#$${ZONE_RESOURCE_ID}#g" -e "s/REGION/${REGION}/g" -e "s/CONSUMER_NAME/${CONSUMER_NAME}/g" deploy/mvp-provisioning-shards.yml > deploy/tmp-provisioning-shard.yml
	ISTO_VERSION=$(shell az aks list --query "[?tags.clusterType == 'svc-cluster' && starts_with(resourceGroup, '${RESOURCEGROUP}')].serviceMeshProfile.istio.revisions[-1]" -o tsv)
	oc process --local -f deploy/openshift-templates/arohcp-namespace-template.yml \
	  -p ISTIO_VERSION=$${ISTO_VERSION} | oc apply -f -
	kubectl apply -f deploy/istio.yml
	oc process --local -f deploy/openshift-templates/arohcp-db-template.yml | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml \
	  -p PROVISION_SHARDS_CONFIG="$$( base64 -i deploy/tmp-provisioning-shard.yml)" | oc apply -f -
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n clusters-service \
			--query clientId) && \
	AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID=${AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID} && \
	CS_SERVICE_PRINCIPAL_CREDS_BASE64='$(shell az keyvault secret show --vault-name "service-kv-aro-hcp-dev" --name "aro-hcp-dev-sp-cs" | jq .value -r | base64 | tr -d '\n')' && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account list --query "[?starts_with(name, 'arohcpoidc')].primaryEndpoints.blob" -g ${REGIONAL_RESOURCEGROUP} -o tsv) && \
	OIDC_WEB_ENDPOINT=$(shell az storage account list --query "[?starts_with(name, 'arohcpoidc')].primaryEndpoints.web" -g ${REGIONAL_RESOURCEGROUP} -o tsv) && \
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
	  -p IMAGE_TAG=cf23767 | oc apply -f -

deploy-integ:
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n clusters-service \
			--query clientId) && \
	oc process --local -f deploy/integration/cluster-service-namespace.yaml \
	-p CLIENT_ID=$${AZURE_CS_MI_CLIENT_ID} | oc apply -f -

# for local development
provision-shard:
	@ZONE_RESOURCE_ID=$(shell az network dns zone list -g ${REGIONAL_RESOURCEGROUP} --query "[?zoneType=='Public'].id" -o tsv) && \
	sed -e "s#ZONE_RESOURCE_ID#$${ZONE_RESOURCE_ID}#g" -e "s/REGION/${REGION}/g" -e "s/CONSUMER_NAME/${CONSUMER_NAME}/g" deploy/dev-provisioning-shards.yml

.PHONY: deploy deploy-integ provision-shard
