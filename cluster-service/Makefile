-include setup-env.mk

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

ZONE_NAME ?= "${REGIONAL_DNS_SUBDOMAIN}.${CX_PARENT_DNS_ZONE_NAME}"
AFD_OIDC_BASE_ENDPOINT ?= "https://${REGIONAL_DNS_SUBDOMAIN}.${OIDC_SUBDOMAIN}.${SVC_PARENT_DNS_ZONE_NAME}/"
TLS_CERTIFICATES_ENABLED ?= true

# PROVISION_SHARDS_IDS_NAMESPACE_UUID is the UUID used as the Namespace ID
# for generating Azure Provision Shard IDs. The value was generated
# using an UUIDv4 with the github.com/google/uuid library.
# *WARNING* Do not change this value as changing it changes how the
# provision shard ids are generated and consumers of the generated names
# rely on predictability of the generated names based on the given input.
PROVISION_SHARDS_IDS_NAMESPACE_UUID=916f9976-e1c0-4afd-b84c-5d5c94fbeaed
# PROVISION_SHARD_ID is the ID of the provision shard for the management cluster.
# It is generated using the PROVISION_SHARDS_IDS_NAMESPACE_UUID and the
# management cluster name(MGMT_AKS_NAME).
# The value is generated using the uuid5 function from the python3 uuid module.
PROVISION_SHARD_ID=$$(python3 -c "import uuid; print(uuid.uuid5(uuid.UUID('${PROVISION_SHARDS_IDS_NAMESPACE_UUID}'), '${MGMT_AKS_NAME}'))")

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
	ZONE_RESOURCE_ID=$(shell az network dns zone show -n ${ZONE_NAME} -g ${REGIONAL_RESOURCEGROUP} --query id -o tsv) && \
	CX_SECRETS_KV_URL="https://${CX_SECRETS_KV_NAME}.vault.azure.net/" && \
	CX_MI_KV_URL="https://${CX_MI_KV_NAME}.vault.azure.net/" && \
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
	  --set shard.consumerName="${CONSUMER_NAME}" \
	  --set shard.zoneResourceId="$${ZONE_RESOURCE_ID}" \
	  --set shard.cxSecretsKeyVaultUrl="$${CX_SECRETS_KV_URL}" \
	  --set shard.cxMiKeyVaultUrl="$${CX_MI_KV_URL}" \
	  --set shard.cxSecretsKeyVaultMiClientId="${CX_SECRETS_KV_MI_CLIENT_ID}" \
	  --set shard.maestroRestUrl="http://maestro.maestro.svc.cluster.local:8000" \
	  --set shard.maestroGrpUrl="maestro-grpc.maestro.svc.cluster.local:8090" \
	  --set shard.Id=$(PROVISION_SHARD_ID) \
	  --set databaseHost=$${DB_HOST} \
	  --set azureMiMockServicePrincipalPrincipalId=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID} \
	  --set azureMiMockServicePrincipalClientId=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID} \
	  --set azureMiMockServicePrincipalCertName=${MI_MOCK_SERVICE_PRINCIPAL_CERT_NAME} \
	  --set azureArmHelperIdentityCertName=${ARM_HELPER_CERT_NAME} \
	  --set azureArmHelperIdentityClientId=${AZURE_ARM_HELPER_IDENTITY_CLIENT_ID} \
	  --set azureArmHelperMockFpaPrincipalId=${AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID} \
	  --set pullBinding.workloadIdentityClientId="$${IMAGE_PULLER_MI_CLIENT_ID}" \
	  --set pullBinding.workloadIdentityTenantId="$${IMAGE_PULLER_MI_TENANT_ID}" \
	  --set pullBinding.registry=${ACR_NAME}.azurecr.io \
	  --set pullBinding.scope=repository:${IMAGE_REPO}:pull \
	  --set managedIdentitiesDataPlaneAudienceResource=${MI_DATAPLANE_AUDIENCE_RESOURCE} \
	  --set tracing.address=${TRACING_ADDRESS} \
	  --set csDeploymentStrategy.rollingUpdate.maxSurge=${CS_DEPLOYMENT_ROLLINGUPDATE_MAX_SURGE} \
	  --set csDeploymentStrategy.rollingUpdate.maxUnavailable=${CS_DEPLOYMENT_ROLLINGUPDATE_MAX_UNAVAILABLE} \
	  --set provisionShardClusterLimit=${PROVISION_SHARD_CLUSTER_LIMIT} \
	  --set ocpVersions.defaultVersion.version=${OCP_VERSIONS_DEFAULT_VERSION_VERSION} \
	  --set ocpVersions.channelGroups.stable.minVersion=${OCP_VERSIONS_CHANNEL_GROUPS_STABLE_MIN_VERSION} \
	  --set ocpVersions.channelGroups.stable.maxVersion=${OCP_VERSIONS_CHANNEL_GROUPS_STABLE_MAX_VERSION} \
	  --set azureOperatorsMI.roleSetName=${OPERATOR_ROLE_SET_NAME} \
	  --set deployment.zoneCount=${AVAILABILITY_ZONE_COUNT}

deploy-cs-debug-jobs:
	./hack/helm.sh cs-debug-jobs helm-charts/cs-debug-jobs ${NAMESPACE} \
	  --set imageDigest=${IMAGE_DIGEST} \
	  --set deployDebugJobs=${DEPLOY_DEBUG_JOBS}

deploy-pr-env-deps:
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n clusters-service --query clientId -o tsv) && \
	oc process --local -f cspr/cluster-service-namespace.yaml \
		-p CLIENT_ID=$${AZURE_CS_MI_CLIENT_ID} \
		-p KEY_VAULT_NAME=${SERVICE_KV} \
		-p FIRST_PARTY_APP_CERTIFICATE_NAME=${FPA_CERT_NAME} \
		-p FPA_CLIENT_ID=${AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID} \
		-p FIRST_PARTY_CHECK_ACCESS_APP_CERTIFICATE_NAME=${FPA_CHECK_ACCESS_CERT_NAME} \
		-p FPA_CHECK_ACCESS_CLIENT_ID=${AZURE_FIRST_PARTY_CHECK_ACCESS_APPLICATION_CLIENT_ID} \
		-p ARM_HELPER_CERTIFICATE_NAME=${ARM_HELPER_CERT_NAME} \
		-p ARM_HELPER_CLIENT_ID=${AZURE_ARM_HELPER_IDENTITY_CLIENT_ID} \
		-p ARM_HELPER_MOCK_FPA_PRINCIPAL_ID=${AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID} \
		-p MSI_MOCK_CERTIFICATE_NAME=${MI_MOCK_SERVICE_PRINCIPAL_CERT_NAME} \
		-p MSI_MOCK_CLIENT_ID=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID} \
		-p MSI_MOCK_PRINCIPAL_ID=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID} | oc apply -f -
	oc process --local -f cspr/orphaned-namespace-cleaner.yaml | oc apply -f -
	./cspr/deploy-resource-cleaner.sh \
		--resource-group "${RESOURCEGROUP}" \
		--cx-keyvault "${CX_SECRETS_KV_NAME}" \
		--mi-keyvault "${CX_MI_KV_NAME}" \
		--acr-name "${OCP_ACR_NAME}"

cspr-jenkins-kubeconfig:
	./cspr-kubeconfig.sh cluster-service-admin cluster-service-mgmt ./cspr.kubeconfig

create-pr-env-sp:
	CLUSTER_ID=$(shell az aks show -g ${RESOURCEGROUP} -n ${AKS_NAME} --query id -o tsv) && \
	az ad sp create-for-rbac \
	--display-name "cs-pr-authentication" \
	--role 'Contributor' \
	--scopes "$${CLUSTER_ID}"
	az role assignment create \
	--role "Key Vault Certificate User" \
	--assignee "$(shell az ad sp list --display-name cs-pr-authentication --query [0].appId -o tsv)" \
	--scope "$(shell az keyvault show --name ${SERVICE_KV} --query id -o tsv)"
	echo "cs-pr-authentication requires 'Microsoft.Authorization/roleAssignments/write' action granted via 'User Access Administrator'. This role assignment requires elevated permissions, please add it in the case of re-creation of this service principal."
#	az role assignment create \
#	--role "User Access Administrator" \
#	--assignee "$(shell az ad sp list --display-name cs-pr-authentication --query [0].appId -o tsv)" \
#	--scope "$${CLUSTER_ID}"

local-deploy-provision-shard:
	@ZONE_RESOURCE_ID=$(shell az network dns zone show -n ${ZONE_NAME} -g ${REGIONAL_RESOURCEGROUP} --query id -o tsv) && \
	CX_SECRETS_KV_URL=$(shell az keyvault show -n ${CX_SECRETS_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	CX_MI_KV_URL=$(shell az keyvault show -n ${CX_MI_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	CX_SECRETS_KV_MI_CLIENT_ID=$(shell az aks show -n ${MGMT_AKS_NAME} -g ${MGMT_RESOURCEGROUP} --query 'addonProfiles.azureKeyvaultSecretsProvider.identity.clientId' -o tsv) && \
	if [ -z "$${CX_SECRETS_KV_MI_CLIENT_ID}" ]; then echo "Failed to get CX_SECRETS_KV_MI_CLIENT_ID - make sure to provision a MGMT cluster first" >&2; exit 1; fi && \
	../templatize.sh $(DEPLOY_ENV) local/provisioning-shards.tmpl.yml local/local-provisioning-shards.yml -e zoneResourceId=$${ZONE_RESOURCE_ID},cxSecretsKeyVaultUrl=$${CX_SECRETS_KV_URL},cxMiKeyVaultUrl=$${CX_MI_KV_URL},cxSecretsKeyVaultMiClientId=$${CX_SECRETS_KV_MI_CLIENT_ID},maestroRestUrl=http://localhost:8001,maestroGrpUrl=localhost:8090,shardId=$(PROVISION_SHARD_ID)
	@cat local/local-provisioning-shards.yml

personal-runtime-config:
	@TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${REGIONAL_RESOURCEGROUP} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell ./oidc-base-endpoint.sh ${OIDC_STORAGE_ACCOUNT} ${REGIONAL_RESOURCEGROUP} ${AFD_OIDC_BASE_ENDPOINT}) && \
	OCP_ACR_URL=$(shell az acr show -n ${OCP_ACR_NAME} --query loginServer -o tsv) && \
	OCP_ACR_RESOURCE_ID=$(shell az acr show -n ${OCP_ACR_NAME} --query id -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) local/azure-runtime-config.tmpl.json local/azure-runtime-config.json \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT},tls_certificates_issuer=$${TLS_CERTIFICATES_ISSUER},tls_certificates_enabled=$(TLS_CERTIFICATES_ENABLED)
	@cat local/azure-runtime-config.json | jq
.PHONY: personal-runtime-config

local-azure-operators-managed-identities-config:
	helm template helm-charts/cluster-service -s templates/azure-operators-managed-identities-config.configmap.yaml \
	  --set azureOperatorsMI.roleSetName=${OPERATOR_ROLE_SET_NAME} \
	  | yq '.data["azure-operators-managed-identities-config.yaml"]' > ./azure-operators-managed-identities-config.yaml
.PHONY: local-azure-operators-managed-identities-config

local-aro-hcp-ocp-versions-config:
	helm template helm-charts/cluster-service \
	--set ocpVersions.defaultVersion.version=${OCP_VERSIONS_DEFAULT_VERSION_VERSION} \
	--set ocpVersions.channelGroups.stable.minVersion=${OCP_VERSIONS_CHANNEL_GROUPS_STABLE_MIN_VERSION} \
	--set ocpVersions.channelGroups.stable.maxVersion=${OCP_VERSIONS_CHANNEL_GROUPS_STABLE_MAX_VERSION} \
	--set azureOperatorsMI.roleSetName=${OPERATOR_ROLE_SET_NAME} \
	-s templates/aro-hcp-ocp-versions-config.configmap.yaml | yq '.data["aro-hcp-ocp-versions-config.yaml"]' > ./aro-hcp-ocp-versions-config.yaml
.PHONY: local-aro-hcp-ocp-versions-config

#
# Shared OIDC Storage
#

SHARED_OIDC_RG_NAME ?= "cs-shared-oidc-storage"
SHARED_OIDC_STORAGE_ACCOUNT_NAME ?= "arohcpdevoidcshared"

shared-oidc-storage:
	@if [ "$$(az group exists --name $(SHARED_OIDC_RG_NAME))" = "false" ]; then \
		az group create \
			--name $(SHARED_OIDC_RG_NAME)  \
			--location westus3 \
			--tags persist=true \
			--output none; \
	fi
	az deployment group create \
		--name oidc \
		--resource-group $(SHARED_OIDC_RG_NAME) \
		--template-file ../dev-infrastructure/modules/oidc/storage.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			location=westus3 \
			accountName=${SHARED_OIDC_STORAGE_ACCOUNT_NAME} \
			isDevEnv=true \
			deploymentScriptLocation=westus3
.PHONY: shared-oidc-storage

shared-storage-runtime-config:
	@TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${SHARED_OIDC_STORAGE_ACCOUNT_NAME} -g ${SHARED_OIDC_RG_NAME} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell az storage account show -n ${SHARED_OIDC_STORAGE_ACCOUNT_NAME} -g ${SHARED_OIDC_RG_NAME} --query primaryEndpoints.web -o tsv) && \
	OCP_ACR_URL=$(shell az acr show -n ${OCP_ACR_NAME} --query loginServer -o tsv) && \
	OCP_ACR_RESOURCE_ID=$(shell az acr show -n ${OCP_ACR_NAME} --query id -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) local/azure-runtime-config.tmpl.json local/azure-runtime-config.json \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT},tls_certificates_issuer=$${TLS_CERTIFICATES_ISSUER},tls_certificates_enabled=$(TLS_CERTIFICATES_ENABLED)
	@cat local/azure-runtime-config.json | jq
.PHONY: shared-storage-runtime-config

.PHONY: create-pr-env-sp deploy deploy-using-azure-db deploy-integ configure-tmp-provision-shard deploy-secrets-template deploy-secrets-template-using-azure-db deploy-istio-configurations-template deploy-namespace-template runtime-config
