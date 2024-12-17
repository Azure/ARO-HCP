-include ../../setup-env.mk
-include ../../helm-cmd.mk
HELM_CMD ?= helm upgrade --install

deploy:
	@kubectl create namespace maestro --dry-run=client -o json | kubectl apply -f -
	@TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	EVENTGRID_HOSTNAME=$(shell az resource show -n ${EVENTGRID_NAME} -g ${REGION_RG} --resource-type "Microsoft.EventGrid/namespaces" --query properties.topicSpacesConfiguration.hostname -o tsv) && \
	MAESTRO_MI_CLIENT_ID=$(shell az identity show -g "${MGMT_RG}" -n maestro-consumer --query clientId -o tsv) && \
	${HELM_CMD} maestro-agent ./helm \
		--namespace maestro \
		--set consumerName=${CONSUMER_NAME} \
		--set broker.host=$${EVENTGRID_HOSTNAME} \
		--set credsKeyVault.name=${KEYVAULT_NAME} \
		--set credsKeyVault.secret=${CONSUMER_NAME} \
		--set azure.clientId=$${MAESTRO_MI_CLIENT_ID} \
		--set azure.tenantId=$${TENANT_ID} \
		--set image.base=${IMAGE_BASE} \
		--set image.tag=${IMAGE_TAG} \
		--set sideCar.imageBase=${SIDECAR_IMAGE_BASE} \
		--set sideCar.imageTag=${SIDECAR_IMAGE_TAG}
.PHONY: deploy
