SHELL = /bin/bash

CONFIG_PROFILE ?= dev
include ../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk

CONSUMER_NAME ?= $(shell az aks list --query "[?tags.clusterType == 'mgmt-cluster' && starts_with(resourceGroup, '$(REGIONAL_RESOURCEGROUP)')].resourceGroup" -o tsv)
EVENTGRID_ID = $(shell az resource list -g ${REGIONAL_RESOURCEGROUP} --resource-type "Microsoft.EventGrid/namespaces" --query "[].id" -o tsv)


MAESTRO_BASE_IMAGE ?= quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro
MAESTRO_TAG ?= ae149df618cb0812d2072b20658a9cff84c087eb

deploy-server:
	MAESTRO_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n maestro-server \
			--query clientId) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	MAESTRO_KV_NAME=$(shell az keyvault list --query "[?starts_with(name, 'maestro-kv')].name" -g ${REGIONAL_RESOURCEGROUP} --output tsv) && \
	EVENTGRID_CA_BASE64='$(shell cat ../dev-infrastructure/scripts/digicert-global-root-g3.crt | base64 | tr -d '\n')' && \
	EVENTGRID_HOSTNAME=$(shell az resource show --ids ${EVENTGRID_ID} --query properties.topicSpacesConfiguration.hostname -o tsv) && \
	ISTO_VERSION=$(shell az aks list --query "[?tags.clusterType == 'svc-cluster' && starts_with(resourceGroup, '${RESOURCEGROUP}')].serviceMeshProfile.istio.revisions[-1]" -o tsv) && \
	kubectl create namespace maestro --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace maestro "istio.io/rev=$${ISTO_VERSION}" && \
	helm upgrade --install maestro-server deploy/helm/server \
		--namespace maestro \
		--set broker.host=$${EVENTGRID_HOSTNAME} \
		--set credsKeyVault.name=$${MAESTRO_KV_NAME} \
		--set azure.clientId=$${MAESTRO_MI_CLIENT_ID} \
		--set azure.tenantId=$${TENANT_ID} \
		--set image.base=${MAESTRO_BASE_IMAGE} \
		--set image.tag=${MAESTRO_TAG} \
		--set database.containerizedDb=true \
		--set database.ssl=disable

deploy-agent:
	MAESTRO_MI_CLIENT_ID=$(shell az identity show \
		-g ${RESOURCEGROUP} \
		-n maestro-consumer \
		--query clientId -o tsv) && \
	TENANT_ID=$(shell az account show --query tenantId -o tsv) && \
	MAESTRO_KV_NAME=$(shell az keyvault list --query "[?starts_with(name, 'maestro-kv')].name" -g ${REGIONAL_RESOURCEGROUP} -o tsv) && \
	EVENTGRID_HOSTNAME=$(shell az resource show --ids ${EVENTGRID_ID} --query properties.topicSpacesConfiguration.hostname -o tsv) && \
	helm upgrade --install maestro-agent deploy/helm/agent \
		--create-namespace --namespace maestro \
		--set consumerName=${CONSUMER_NAME} \
		--set broker.host=$${EVENTGRID_HOSTNAME} \
		--set credsKeyVault.name=$${MAESTRO_KV_NAME} \
		--set credsKeyVault.secret=${CONSUMER_NAME} \
		--set azure.clientId=$${MAESTRO_MI_CLIENT_ID} \
		--set azure.tenantId=$${TENANT_ID} \
		--set image.base=${MAESTRO_BASE_IMAGE} \
		--set image.tag=${MAESTRO_TAG}

register-agent:
	./register-maestro-consumer.sh ${CONSUMER_NAME}

# still WIP
register-agent-job:
	oc process --local -f deploy/openshift-templates/agent-registration.yml \
	  -p MAESTRO_AGENT_CONSUMER_NAME=${CONSUMER_NAME} | oc apply -f -

.PHONY: deploy-server deploy-agent register-agent register-agent-job
