-include ../setup-env.mk
-include ../helm-cmd.mk

deploy:
	CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n ${MI_NAME} --query clientId -o tsv) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	${HELM_CMD} arobit \
		deploy/ \
		--namespace arobit \
		--set forwarder.fluentbit.image.registry=${AROBIT_FORWARDER_REGISTRY} \
		--set forwarder.fluentbit.image.repository=${AROBIT_FORWARDER_REPOSITORY} \
		--set forwarder.fluentbit.image.digest=${AROBIT_FORWARDER_DIGEST} \
		--set forwarder.mdsd.image.registry=${AROBIT_MDSD_REGISTRY} \
		--set forwarder.mdsd.image.repository=${AROBIT_MDSD_REPOSITORY} \
		--set forwarder.mdsd.image.digest=${AROBIT_MDSD_DIGEST} \
		--set forwarder.mdsd.geneva.region=${REGION} \
		--set forwarder.mdsd.geneva.environment=${GENEVA_ENVIRONMENT} \
		--set forwarder.clusterType=${AROBIT_CLUSTER_TYPE} \
		--set forwarder.mdsd.enabled=${AROBIT_MDSD_ENABLED} \
		--set forwarder.secretProvider.keyVault=${AROBIT_SECRET_PROVIDER_KEY_VAULT} \
		--set forwarder.secretProvider.msiClientId=$${CLIENT_ID} \
		--set forwarder.secretProvider.tenantId=$${TENANT_ID} \
		--set forwarder.mdsd.geneva.rpAccountName=${AROBIT_GENEVA_RP_ACCOUNT_NAME} \
		--set forwarder.mdsd.geneva.rpSecretName=${AROBIT_GENEVA_RP_SECRET_NAME} \
		--set forwarder.mdsd.geneva.rpSan=${AROBIT_GENEVA_RP_SAN} \
		--set forwarder.mdsd.geneva.rpNamespace=${AROBIT_GENEVA_RP_NAMESPACE} \
		--set forwarder.mdsd.geneva.clusterLogsAccountName=${AROBIT_GENEVA_CLUSTER_LOGS_ACCOUNT_NAME} \
		--set forwarder.mdsd.geneva.clusterLogsSecretName=${AROBIT_GENEVA_CLUSTER_LOGS_SECRET_NAME} \
		--set forwarder.mdsd.geneva.clusterLogsSan=${AROBIT_GENEVA_CLUSTER_LOGS_SAN} \
		--set forwarder.mdsd.geneva.clusterLogsNamespace=${AROBIT_GENEVA_CLUSTER_LOGS_NAMESPACE}
.PHONY: deploy

undeploy:
	helm uninstall arobit --namespace arobit
.PHONY: undeploy
