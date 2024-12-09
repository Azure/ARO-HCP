SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk

deploy:
	kubectl create namespace maestro --dry-run=client -o json | kubectl apply -f -
	ISTO_VERSION=$(shell az aks show -n ${AKS_NAME} -g ${SVC_RG} --query serviceMeshProfile.istio.revisions[-1] -o tsv) && \
	kubectl label namespace maestro "istio.io/rev=$${ISTO_VERSION}" --overwrite=true
	EVENTGRID_HOSTNAME=$(shell az resource show -n ${EVENTGRID_NAME} -g ${REGION_RG} --resource-type "Microsoft.EventGrid/namespaces" --query properties.topicSpacesConfiguration.hostname -o tsv) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	MAESTRO_MI_CLIENT_ID=$(shell az identity show -g "${SVC_RG}" -n maestro-server --query clientId -o tsv) && \
	helm upgrade --install maestro-server ./helm \
		--namespace maestro \
		--set broker.host=$${EVENTGRID_HOSTNAME} \
		--set credsKeyVault.name=${KEYVAULT_NAME} \
		--set credsKeyVault.secret=${MQTT_CLIENT_NAME} \
		--set azure.clientId=$${MAESTRO_MI_CLIENT_ID} \
		--set azure.tenantId=$${TENANT_ID} \
		--set istio.restrictIngress=${ISTIO_RESTRICT_INGRESS} \
		--set image.base=${IMAGE_BASE} \
		--set image.tag=${IMAGE_TAG} \
		--set database.containerizedDb=${USE_CONTAINERIZED_DB} \
		--set database.ssl='${USE_DATABASE_SSL}'
.PHONY: deploy
