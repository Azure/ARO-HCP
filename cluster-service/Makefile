SHELL = /bin/bash
AKSCONFIG ?= svc-cluster

#IMAGE_REPOSITORY?=app-sre/uhc-clusters-service
IMAGE_REPOSITORY?=cluster-service/uhc-clusters-service
IMAGE_TAG?=1723149695

CONFIG_PROFILE ?= dev
include ../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk

CONSUMER_NAME ?= $(shell az aks list --query "[?tags.clusterType == 'mgmt-cluster' && starts_with(resourceGroup, '$(REGIONAL_RESOURCEGROUP)')].resourceGroup" -o tsv)

deploy:
	ZONE_RESOURCE_ID=$(shell az network dns zone list -g ${REGIONAL_RESOURCEGROUP} --query "[?zoneType=='Public'].id" -o tsv) && \
	sed -e "s#ZONE_RESOURCE_ID#$${ZONE_RESOURCE_ID}#g" -e "s/REGION/${REGION}/g" -e "s/CONSUMER_NAME/${CONSUMER_NAME}/g" deploy/mvp-provisioning-shards.yml > deploy/tmp-provisioning-shard.yml
	oc process --local -f deploy/openshift-templates/arohcp-namespace-template.yml \
	  -p ISTIO_VERSION=asm-1-20 | oc apply -f -
	kubectl apply -f deploy/istio.yml

	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml \
		-p DB_HOST=$(shell az postgres flexible-server list --resource-group ${RESOURCEGROUP} --query "[?starts_with(name, 'cs-pg-')].fullyQualifiedDomainName" -o tsv) \
		-p DB_NAME=clusters-service \
		-p DB_USER=clusters-service \
		-p DB_PASSWORD=fetched-at-runtime \
	  	-p PROVISION_SHARDS_CONFIG="$$( base64 -i deploy/tmp-provisioning-shard.yml)" | oc apply -f -

	AZURE_CS_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n clusters-service \
			--query clientId) && \
	CS_SERVICE_PRINCIPAL_CREDS_BASE64='$(shell az keyvault secret show --vault-name "service-kv-aro-hcp-dev" --name "aro-hcp-dev-sp-cs" | jq .value -r | base64 | tr -d '\n')' && \
	oc process --local -f deploy/openshift-templates/arohcp-service-template.yml \
	  -p AZURE_CS_MI_CLIENT_ID=$${AZURE_CS_MI_CLIENT_ID} \
	  -p REGION=${REGION} \
	  -p CS_SERVICE_PRINCIPAL_CREDS_BASE64=$${CS_SERVICE_PRINCIPAL_CREDS_BASE64} \
	  -p IMAGE_REGISTRY=${ARO_HCP_IMAGE_ACR}.azurecr.io \
	  -p IMAGE_REPOSITORY=${IMAGE_REPOSITORY} \
	  -p IMAGE_TAG=${IMAGE_TAG} | oc apply -f -

# for local development
provision-shard:
	@ZONE_RESOURCE_ID=$(shell az network dns zone list -g ${REGIONAL_RESOURCEGROUP} --query "[?zoneType=='Public'].id" -o tsv) && \
	sed -e "s#ZONE_RESOURCE_ID#$${ZONE_RESOURCE_ID}#g" -e "s/REGION/${REGION}/g" -e "s/CONSUMER_NAME/${CONSUMER_NAME}/g" deploy/dev-provisioning-shards.yml

.PHONY: deploy
