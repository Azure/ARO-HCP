SHELL = /bin/bash

RESOURCEGROUP ?= aro-hcp-${AKSCONFIG}-$(USER)
MAESTRO_INFRA_RESOURCEGROUP ?= RESOURCEGROUP
REGION ?= $(shell az group show -n ${RESOURCEGROUP} --query location)

deploy-server:
	MAESTRO_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n maestro-server-${REGION} \
			--query clientId) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	MAESTRO_KV_NAME=$(shell az keyvault list --query "[?starts_with(name, 'maestro-kv')].name" -g ${MAESTRO_INFRA_RESOURCEGROUP} --output tsv) && \
	oc process --local -f deploy/openshift-templates/namespace-template.yml \
	  -p ISTIO_VERSION=asm-1-20 | oc apply -f - && \
	oc process --local -f deploy/openshift-templates/db-template.yml | oc apply -f - && \
	oc process --local -f deploy/openshift-templates/service-template.yml \
	  -p IMAGE_REGISTRY=quay.io \
	  -p IMAGE_REPOSITORY=redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro \
	  -p IMAGE_TAG=ae149df618cb0812d2072b20658a9cff84c087eb \
	  -p DB_SSLMODE=disable \
	  -p DB_SECRET_NAME=maestro-db \
	  -p MAESTRO_MI_CLIENT_ID=$${MAESTRO_MI_CLIENT_ID} \
	  -p MAESTRO_KV_NAME=$${MAESTRO_KV_NAME} \
	  -p AZURE_TENANT_ID=$${TENANT_ID} | oc apply -f -

deploy-agent:
	MAESTRO_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n maestro-consumer-${REGION} \
			--query clientId) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	MAESTRO_KV_NAME=$(shell az keyvault list --query "[?starts_with(name, 'maestro-kv')].name" -g ${MAESTRO_INFRA_RESOURCEGROUP} --output tsv) && \
	oc process --local -f deploy/openshift-templates/agent-template.yml \
	  -p IMAGE_REGISTRY=quay.io \
	  -p IMAGE_REPOSITORY=redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro \
	  -p IMAGE_TAG=ae149df618cb0812d2072b20658a9cff84c087eb \
	  -p MAESTRO_MI_CLIENT_ID=$${MAESTRO_MI_CLIENT_ID} \
	  -p MAESTRO_KV_NAME=$${MAESTRO_KV_NAME} \
	  -p AZURE_TENANT_ID=$${TENANT_ID} | oc apply -f -

.PHONY: deploy-server
