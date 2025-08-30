ZONE_NAME ?= "${REGIONAL_DNS_SUBDOMAIN}.${CX_PARENT_DNS_ZONE_NAME}"

deploy:
	@if ! kubectl get namespace ${NAMESPACE} > /dev/null 2>&1; then \
		echo "Error: Namespace '${NAMESPACE}' not found"; \
		exit 1; \
	fi
	ZONE_RESOURCE_ID=$(shell az network dns zone show -n ${ZONE_NAME} -g ${REGIONAL_RESOURCEGROUP} --query id -o tsv) && \
	CX_SECRETS_KV_URL="https://${CX_SECRETS_KV_NAME}.vault.azure.net/" && \
	CX_MI_KV_URL="https://${CX_MI_KV_NAME}.vault.azure.net/" && \
	OUTBOUND_IP=$(shell az resource show --ids ${AKS_OUTBOUND_IP_RESOURCE_ID} --query "properties.ipAddress" -o tsv) && \
	../../hack/helm.sh ${CONSUMER_NAME} cs ${NAMESPACE} \
	    --set serviceAccountName.serviceAccountName=${SERVICE_ACCOUNT_NAME} \
		--set shard.id=1 \
		--set shard.consumerName=${CONSUMER_NAME} \
		--set shard.zoneResourceId=$${ZONE_RESOURCE_ID} \
		--set shard.cxSecretsKeyVaultUrl=$${CX_SECRETS_KV_URL} \
		--set shard.cxMiKeyVaultUrl=$${CX_MI_KV_URL} \
		--set shard.cxSecretsKeyVaultMiClientId=${CX_SECRETS_KV_MI_CLIENT_ID} \
		--set shard.maestroRestUrl="http://maestro.maestro.svc.cluster.local:8000" \
	  	--set shard.maestroGrpUrl="maestro-grpc.maestro.svc.cluster.local:8090" \
		--set shard.outboundIP=$${OUTBOUND_IP} \
		--set shard.region=${REGION}
.PHONY: deploy
