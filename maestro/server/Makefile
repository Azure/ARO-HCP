-include ../../setup-env.mk

deploy:
	@kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f -
	@kubectl label ${KUBECTL_DRY_RUN} namespace ${NAMESPACE} "istio.io/rev=${ISTO_TAG}" --overwrite=true
	@EVENTGRID_HOSTNAME=$(shell az resource show -n ${EVENTGRID_NAME} -g ${REGION_RG} --resource-type "Microsoft.EventGrid/namespaces" --query properties.topicSpacesConfiguration.hostname -o tsv) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	MAESTRO_MI_CLIENT_ID=$(shell az identity show -g "${SVC_RG}" -n ${MANAGED_IDENTITY_NAME} --query clientId -o tsv) && \
	DATABASE_HOST=$$(if [ "${USE_AZURE_DB}" = "true" ]; then az postgres flexible-server show -g ${REGION_RG} -n ${DATABASE_SERVER_NAME} --query fullyQualifiedDomainName -o tsv; else echo "maestro-db"; fi) && \
	OVERRIDES=$$(if [ "${USE_AZURE_DB}" = "true" ]; then echo "azuredb.values.yaml"; else echo "containerdb.values.yaml"; fi) && \
	IMAGE_PULLER_MI_CLIENT_ID=$(shell az identity show -g ${SVC_RG} -n image-puller --query clientId -o tsv) && \
	../../hack/helm.sh maestro-server deploy ${NAMESPACE} \
		-f deploy/$${OVERRIDES} \
		--set maestro.serviceAccount=${SERVICE_ACCOUNT_NAME} \
		--set maestro.glog_v=${MAESTRO_LOG_LEVEL} \
		--set broker.host=$${EVENTGRID_HOSTNAME} \
		--set credsKeyVault.name=${KEYVAULT_NAME} \
		--set credsKeyVault.secret=${MQTT_CLIENT_NAME} \
		--set azure.clientId=$${MAESTRO_MI_CLIENT_ID} \
		--set azure.tenantId=$${TENANT_ID} \
		--set istio.restrictIngress=${ISTIO_RESTRICT_INGRESS} \
		--set image.digest=${IMAGE_DIGEST} \
		--set image.registry=${ACR_NAME}.azurecr.io \
		--set image.repository=${IMAGE_REPO} \
		--set database.host=$${DATABASE_HOST} \
		--set database.name=${DATABASE_NAME} \
		--set clustersService.namespace=${CS_NAMESPACE} \
		--set clustersService.serviceAccount=${CS_SERVICE_ACCOUNT_NAME} \
		--set pullBinding.workloadIdentityClientId="$${IMAGE_PULLER_MI_CLIENT_ID}" \
		--set pullBinding.workloadIdentityTenantId="$${TENANT_ID}" \
		--set pullBinding.registry=${ACR_NAME}.azurecr.io \
		--set tracing.address=${TRACING_ADDRESS} \
		--set tracing.exporter=${TRACING_EXPORTER} \
		--set pullBinding.scope=repository:${IMAGE_BASE}:pull

.PHONY: deploy
