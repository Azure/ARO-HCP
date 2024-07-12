SHELL = /bin/bash

RESOURCEGROUP ?= aro-hcp-${AKSCONFIG}-$(USER)
REGION ?= $(shell az group show -n ${RESOURCEGROUP} --query location -o tsv)
IMAGE_TAG ?= 1720810722

deploy:
	DOMAIN=$(shell az network dns zone list -g ${RESOURCEGROUP} --query "[?zoneType=='Public'].name" -o tsv) && \
	sed -e "s/DOMAIN/$${DOMAIN}/g" -e "s/REGION/${REGION}/g" deploy/mvp-provisioning-shards.yml > deploy/tmp-provisioning-shard.yml
	oc process --local -f deploy/openshift-templates/arohcp-namespace-template.yml \
	  -p ISTIO_VERSION=asm-1-20 | oc apply -f -

	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml \
		-p DB_HOST=$(shell az postgres flexible-server list --resource-group ${RESOURCEGROUP} --query "[?starts_with(name, 'cs-pg-')].fullyQualifiedDomainName" -o tsv) \
		-p DB_NAME=cluster-service \
		-p DB_USER=clusters-service \
		-p DB_PASSWORD=fetched-at-runtime \
	  	-p PROVISION_SHARDS_CONFIG="$$( base64 -i deploy/tmp-provisioning-shard.yml)" | oc apply -f -

	AZURE_CS_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n clusters-service-${REGION} \
			--query clientId) && \
	oc process --local -f deploy/openshift-templates/arohcp-service-template.yml \
	  -p AZURE_CS_MI_CLIENT_ID=$${AZURE_CS_MI_CLIENT_ID} \
	  -p IMAGE_REGISTRY=devarohcp.azurecr.io \
	  -p IMAGE_REPOSITORY=cluster-service/uhc-clusters-service \
	  -p IMAGE_TAG=${IMAGE_TAG} | oc apply -f -

.PHONY: deploy