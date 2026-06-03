export OP_CLUSTER_API_AZURE_ROLE_NAME
export OP_CONTROL_PLANE_ROLE_NAME
export OP_CLOUD_CONTROLLER_MANAGER_ROLE_NAME
export OP_INGRESS_ROLE_NAME
export OP_DISK_CSI_DRIVER_ROLE_NAME
export OP_FILE_CSI_DRIVER_ROLE_NAME
export OP_IMAGE_REGISTRY_DRIVER_ROLE_NAME
export OP_CLOUD_NETWORK_CONFIG_ROLE_NAME
export OP_KMS_ROLE_NAME
export TLS_CERTIFICATES_ISSUER

AFD_OIDC_BASE_ENDPOINT ?= "https://${REGIONAL_DNS_SUBDOMAIN}.${OIDC_SUBDOMAIN}.${SVC_PARENT_DNS_ZONE_NAME}/"
TLS_CERTIFICATES_ENABLED ?= true

deploy:
	kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f - && \
	IMAGE_PULLER_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n image-puller --query clientId -o tsv) && \
	IMAGE_PULLER_MI_TENANT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n image-puller --query tenantId -o tsv) && \
	kubectl label namespace ${NAMESPACE} "istio.io/rev=${ISTO_TAG}" --overwrite=true && \
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n ${MI_NAME} --query clientId -o tsv) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${REGIONAL_RESOURCEGROUP} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell ./oidc-base-endpoint.sh ${OIDC_STORAGE_ACCOUNT} ${REGIONAL_RESOURCEGROUP} ${AFD_OIDC_BASE_ENDPOINT}) && \
	DB_HOST=$$(if [ "${USE_AZURE_DB}" = "true" ]; then az postgres flexible-server show -g ${REGIONAL_RESOURCEGROUP} -n ${DATABASE_SERVER_NAME} --query fullyQualifiedDomainName -o tsv; else echo "ocm-cs-db"; fi) && \
	OVERRIDES=$$(if [ "${USE_AZURE_DB}" = "true" ]; then echo "azuredb.values.yaml"; else echo "containerdb.values.yaml"; fi) && \
	./hack/helm.sh cluster-service helm-charts/cluster-service ${NAMESPACE} \
	  -f helm-charts/cluster-service/$${OVERRIDES} \
	  --set serviceAccountName=${SERVICE_ACCOUNT_NAME} \
	  --set environment=${ENVIRONMENT} \
	  --set azureCsMiClientId=$${AZURE_CS_MI_CLIENT_ID} \
	  --set oidcIssuerBlobServiceUrl=$${OIDC_BLOB_SERVICE_ENDPOINT} \
	  --set oidcIssuerBaseUrl=$${OIDC_ISSUER_BASE_ENDPOINT} \
	  --set tlsCertificatesIssuer=$${TLS_CERTIFICATES_ISSUER} \
	  --set tlsCertificatesEnabled=$(TLS_CERTIFICATES_ENABLED) \
	  --set denyAssignments=$(DENYASSIGNMENTS) \
	  --set tenantId=$${TENANT_ID} \
	  --set region=${REGION} \
	  --set serviceKeyvaultName=${SERVICE_KV} \
	  --set replicas=${REPLICAS} \
	  --set imageRegistry=${ACR_NAME}.azurecr.io \
	  --set imageRepository=${IMAGE_REPO} \
	  --set imageDigest=${IMAGE_DIGEST} \
	  --set global.imageDigest=${IMAGE_DIGEST} \
	  --set azureFirstPartyApplicationClientId=${AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID} \
	  --set batchProcessesDryRun=${CS_BATCH_PROCESSES_DRY_RUN} \
	  --set batchProcesses="${CS_BATCH_PROCESSES}" \
	  --set fpaCertName=${FPA_CERT_NAME} \
	  --set ocpAcrResourceId=${OCP_ACR_RESOURCE_ID} \
	  --set ocpAcrUrl=${OCP_ACR_URL} \
	  --set databaseHost=$${DB_HOST} \
	  --set azureMiMockServicePrincipalPrincipalId=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID} \
	  --set azureMiMockServicePrincipalClientId=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID} \
	  --set azureMiMockServicePrincipalCertName=${MI_MOCK_SERVICE_PRINCIPAL_CERT_NAME} \
	  --set azureArmHelperIdentityCertName=${ARM_HELPER_CERT_NAME} \
	  --set azureArmHelperIdentityClientId=${AZURE_ARM_HELPER_IDENTITY_CLIENT_ID} \
	  --set azureArmHelperMockFpaPrincipalId=${AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID} \
	  --set azureCloudEnvironmentName=${AZURE_CLOUD_ENVIRONMENT_NAME} \
	  --set pullBinding.workloadIdentityClientId="$${IMAGE_PULLER_MI_CLIENT_ID}" \
	  --set pullBinding.workloadIdentityTenantId="$${IMAGE_PULLER_MI_TENANT_ID}" \
	  --set pullBinding.registry=${ACR_NAME}.azurecr.io \
	  --set pullBinding.scope=repository:${IMAGE_REPO}:pull \
	  --set managedIdentitiesDataPlaneAudienceResource=${MI_DATAPLANE_AUDIENCE_RESOURCE} \
	  --set tracing.address=${TRACING_ADDRESS} \
	  --set csDeploymentStrategy.rollingUpdate.maxSurge=${CS_DEPLOYMENT_ROLLINGUPDATE_MAX_SURGE} \
	  --set csDeploymentStrategy.rollingUpdate.maxUnavailable=${CS_DEPLOYMENT_ROLLINGUPDATE_MAX_UNAVAILABLE} \
	  --set azureOperatorsMI.roleSetName=${OPERATOR_ROLE_SET_NAME} \
	  --set deployment.zoneCount=${AVAILABILITY_ZONE_COUNT} \
	  --set memoryRequest=${CS_MEMORY_REQUEST} \
	  --set cpuRequest=${CS_CPU_REQUEST} \
	  --set memoryLimit=${CS_MEMORY_LIMIT}

deploy-cs-debug-jobs:
	./hack/helm.sh cs-debug-jobs helm-charts/cs-debug-jobs ${NAMESPACE} \
	  --set imageDigest=${IMAGE_DIGEST} \
	  --set deployDebugJobs=${DEPLOY_DEBUG_JOBS}

.PHONY: deploy deploy-cs-debug-jobs
