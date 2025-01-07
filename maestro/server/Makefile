-include ../../setup-env.mk

deploy:
	@kubectl create namespace maestro --dry-run=client -o json | kubectl apply -f -
	@kubectl label ${KUBECTL_DRY_RUN} namespace maestro "istio.io/rev=${ISTO_TAG}" --overwrite=true
	@EVENTGRID_HOSTNAME=$(shell az resource show -n ${EVENTGRID_NAME} -g ${REGION_RG} --resource-type "Microsoft.EventGrid/namespaces" --query properties.topicSpacesConfiguration.hostname -o tsv) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	MAESTRO_MI_CLIENT_ID=$(shell az identity show -g "${SVC_RG}" -n maestro-server --query clientId -o tsv) && \
	DATABASE_HOST=$$(if [ "${USE_AZURE_DB}" = "true" ]; then az postgres flexible-server show -g ${SVC_RG} -n ${DATABASE_NAME} --query fullyQualifiedDomainName -o tsv; else echo "maestro-db"; fi) && \
	OVERRIDES=$$(if [ "${USE_AZURE_DB}" = "true" ]; then echo "azuredb.values.yaml"; else echo "containerdb.values.yaml"; fi) && \
	helm upgrade --install --wait ${HELM_DRY_RUN} maestro-server ./helm \
		--namespace maestro \
		-f helm/$${OVERRIDES} \
		--set broker.host=$${EVENTGRID_HOSTNAME} \
		--set credsKeyVault.name=${KEYVAULT_NAME} \
		--set credsKeyVault.secret=${MQTT_CLIENT_NAME} \
		--set azure.clientId=$${MAESTRO_MI_CLIENT_ID} \
		--set azure.tenantId=$${TENANT_ID} \
		--set istio.restrictIngress=${ISTIO_RESTRICT_INGRESS} \
		--set image.base=${IMAGE_BASE} \
		--set image.tag=${IMAGE_TAG} \
		--set database.host=$${DATABASE_HOST}
.PHONY: deploy
