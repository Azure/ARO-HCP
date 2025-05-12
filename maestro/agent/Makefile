-include ../../setup-env.mk

deploy:
	@kubectl create namespace maestro --dry-run=client -o json | kubectl apply -f -
	@TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	MAESTRO_MI_CLIENT_ID=$(shell az identity show -g "${MGMT_RG}" -n maestro-consumer --query clientId -o tsv) && \
	../../hack/helm.sh maestro-agent deploy maestro \
		--set consumerName=${CONSUMER_NAME} \
		--set glog_v=${MAESTRO_LOG_LEVEL} \
		--set broker.host=${EVENTGRID_HOSTNAME} \
		--set credsKeyVault.name=${KEYVAULT_NAME} \
		--set credsKeyVault.secret=${CONSUMER_NAME} \
		--set azure.clientId=$${MAESTRO_MI_CLIENT_ID} \
		--set azure.tenantId=$${TENANT_ID} \
		--set image.registry=${ACR_NAME}.azurecr.io \
		--set image.repository=${IMAGE_REPO} \
		--set image.digest=${IMAGE_DIGEST} \
		--set sideCar.image.registry=${SIDECAR_IMAGE_REGISTRY} \
		--set sideCar.image.repository=${SIDECAR_IMAGE_REPOSITORY} \
		--set sideCar.image.digest=${SIDECAR_IMAGE_DIGEST}
.PHONY: deploy
