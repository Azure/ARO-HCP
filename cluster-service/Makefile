SHELL = /bin/bash

RESOURCEGROUP ?= aro-hcp-${AKSCONFIG}-$(USER)
REGION ?= $(shell az group show -n ${RESOURCEGROUP} --query location)

deploy:
	DOMAIN=$(shell az network dns zone list -g ${RESOURCEGROUP} --query "[?zoneType=='Public'].name" -o tsv) && \
	sed "s/DOMAIN/$${DOMAIN}/g" deploy/mvp-provisioning-shards.yml > deploy/tmp-provisioning-shard.yml && \
	sed -i '' "s/REGION/${REGION}/g" deploy/tmp-provisioning-shard.yml
	oc process --local -f deploy/openshift-templates/arohcp-namespace-template.yml \
	  -p ISTIO_VERSION=asm-1-20 | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-db-template.yml | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml \
	  -p PROVISION_SHARDS_CONFIG="$$( base64 -i deploy/tmp-provisioning-shard.yml)" | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-service-template.yml \
	  -p IMAGE_REGISTRY=devarohcp.azurecr.io \
	  -p IMAGE_REPOSITORY=app-sre/uhc-clusters-service \
	  -p IMAGE_TAG=4eef113 | oc apply -f -

.PHONY: deploy
