-include ../setup-env.mk
-include ../helm-cmd.mk

ZONE_NAME ?= "${REGIONAL_DNS_SUBDOMAIN}.${CX_PARENT_DNS_ZONE_NAME}"


deploy:
	@kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f - && \
	IMAGE_PULLER_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n image-puller --query clientId -o tsv) && \
	IMAGE_PULLER_MI_TENANT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n image-puller --query tenantId -o tsv) && \
	kubectl label namespace ${NAMESPACE} "istio.io/rev=${ISTO_TAG}" --overwrite=true && \
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n ${MI_NAME} --query clientId -o tsv) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.web -o tsv) && \
	OCP_ACR_URL=$(shell az acr show -n ${OCP_ACR_NAME} --query loginServer -o tsv) && \
	OCP_ACR_RESOURCE_ID=$(shell az acr show -n ${OCP_ACR_NAME} --query id -o tsv) && \
	DB_HOST=$$(if [ "${USE_AZURE_DB}" = "true" ]; then az postgres flexible-server show -g ${RESOURCEGROUP} -n ${DATABASE_SERVER_NAME} --query fullyQualifiedDomainName -o tsv; else echo "ocm-cs-db"; fi) && \
	OVERRIDES=$$(if [ "${USE_AZURE_DB}" = "true" ]; then echo "azuredb.values.yaml"; else echo "containerdb.values.yaml"; fi) && \
	OP_CLUSTER_API_AZURE_ROLE_ID=$(shell az role definition list --name "${OP_CLUSTER_API_AZURE_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_CONTROL_PLANE_ROLE_ID=$(shell az role definition list --name "${OP_CONTROL_PLANE_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_CLOUD_CONTROLLER_MANAGER_ROLE_ID=$(shell az role definition list --name "${OP_CLOUD_CONTROLLER_MANAGER_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_INGRESS_ROLE_ID=$(shell az role definition list --name "${OP_INGRESS_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_DISK_CSI_DRIVER_ROLE_ID=$(shell az role definition list --name "${OP_DISK_CSI_DRIVER_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_FILE_CSI_DRIVER_ROLE_ID=$(shell az role definition list --name "${OP_FILE_CSI_DRIVER_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_IMAGE_REGISTRY_DRIVER_ROLE_ID=$(shell az role definition list --name "${OP_IMAGE_REGISTRY_DRIVER_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_CLOUD_NETWORK_CONFIG_ROLE_ID=$(shell az role definition list --name "${OP_CLOUD_NETWORK_CONFIG_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_KMS_ROLE_ID=$(shell az role definition list --name "${OP_KMS_ROLE_NAME}" --query "[].name" -o tsv) && \
	ZONE_RESOURCE_ID=$(shell az network dns zone show -n ${ZONE_NAME} -g ${REGIONAL_RESOURCEGROUP} --query id -o tsv) && \
	CX_SECRETS_KV_URL=$(shell az keyvault show -n ${CX_SECRETS_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	CX_MI_KV_URL=$(shell az keyvault show -n ${CX_MI_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	${HELM_CMD} cluster-service deploy \
	  --namespace ${NAMESPACE} \
	  -f deploy/$${OVERRIDES} \
	  --set serviceAccountName=${SERVICE_ACCOUNT_NAME} \
	  --set environment=${ENVIRONMENT} \
	  --set azureCsMiClientId=$${AZURE_CS_MI_CLIENT_ID} \
	  --set oidcIssuerBlobServiceUrl=$${OIDC_BLOB_SERVICE_ENDPOINT} \
	  --set oidcIssuerBaseUrl=$${OIDC_ISSUER_BASE_ENDPOINT} \
	  --set tenantId=$${TENANT_ID} \
	  --set region=${REGION} \
	  --set serviceKeyvaultName=${SERVICE_KV} \
	  --set imageRegistry=${ACR_NAME}.azurecr.io \
	  --set imageRepository=${IMAGE_REPO} \
	  --set imageDigest=${IMAGE_DIGEST} \
	  --set azureFirstPartyApplicationClientId=${AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID} \
	  --set fpaCertName=${FPA_CERT_NAME} \
	  --set ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID} \
	  --set ocpAcrUrl=$${OCP_ACR_URL} \
	  --set shard.consumerName="${CONSUMER_NAME}" \
	  --set shard.zoneResourceId="$${ZONE_RESOURCE_ID}" \
	  --set shard.cxSecretsKeyVaultUrl="$${CX_SECRETS_KV_URL}" \
	  --set shard.cxMiKeyVaultUrl="$${CX_MI_KV_URL}" \
	  --set shard.maestroRestUrl="http://maestro.maestro.svc.cluster.local:8000" \
	  --set shard.maestroGrpUrl="maestro-grpc.maestro.svc.cluster.local:8090" \
	  --set databaseHost=$${DB_HOST} \
	  --set azureMiMockServicePrincipalPrincipalId=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID} \
	  --set azureMiMockServicePrincipalClientId=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID} \
	  --set azureMiMockServicePrincipalCertName=${MI_MOCK_SERVICE_PRINCIPAL_CERT_NAME} \
	  --set azureArmHelperIdentityCertName=${ARM_HELPER_CERT_NAME} \
	  --set azureArmHelperIdentityClientId=${AZURE_ARM_HELPER_IDENTITY_CLIENT_ID} \
	  --set azureArmHelperMockFpaPrincipalId=${AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID} \
	  --set azureOperatorsMI.clusterApiAzure.roleName="${OP_CLUSTER_API_AZURE_ROLE_NAME}" \
	  --set azureOperatorsMI.clusterApiAzure.roleId="$${OP_CLUSTER_API_AZURE_ROLE_ID}" \
	  --set azureOperatorsMI.controlPlane.roleName="${OP_CONTROL_PLANE_ROLE_NAME}" \
	  --set azureOperatorsMI.controlPlane.roleId="$${OP_CONTROL_PLANE_ROLE_ID}" \
	  --set azureOperatorsMI.cloudControllerManager.roleName="${OP_CLOUD_CONTROLLER_MANAGER_ROLE_NAME}" \
	  --set azureOperatorsMI.cloudControllerManager.roleId="$${OP_CLOUD_CONTROLLER_MANAGER_ROLE_ID}" \
	  --set azureOperatorsMI.ingress.roleName="${OP_INGRESS_ROLE_NAME}" \
	  --set azureOperatorsMI.ingress.roleId="$${OP_INGRESS_ROLE_ID}" \
	  --set azureOperatorsMI.diskCsiDriver.roleName="${OP_DISK_CSI_DRIVER_ROLE_NAME}" \
	  --set azureOperatorsMI.diskCsiDriver.roleId="$${OP_DISK_CSI_DRIVER_ROLE_ID}" \
	  --set azureOperatorsMI.fileCsiDriver.roleName="${OP_FILE_CSI_DRIVER_ROLE_NAME}" \
	  --set azureOperatorsMI.fileCsiDriver.roleId="$${OP_FILE_CSI_DRIVER_ROLE_ID}" \
	  --set azureOperatorsMI.imageRegistry.roleName="${OP_IMAGE_REGISTRY_DRIVER_ROLE_NAME}" \
	  --set azureOperatorsMI.imageRegistry.roleId="$${OP_IMAGE_REGISTRY_DRIVER_ROLE_ID}" \
	  --set azureOperatorsMI.cloudNetworkConfig.roleName="${OP_CLOUD_NETWORK_CONFIG_ROLE_NAME}" \
	  --set azureOperatorsMI.cloudNetworkConfig.roleId="$${OP_CLOUD_NETWORK_CONFIG_ROLE_ID}" \
	  --set pullBinding.workloadIdentityClientId="$${IMAGE_PULLER_MI_CLIENT_ID}" \
	  --set pullBinding.workloadIdentityTenantId="$${IMAGE_PULLER_MI_TENANT_ID}" \
	  --set pullBinding.registry=${ACR_NAME}.azurecr.io \
	  --set pullBinding.scope=repository:${IMAGE_REPO}:pull \
	  --set azureOperatorsMI.kms.roleName="${OP_KMS_ROLE_NAME}" \
      --set azureOperatorsMI.kms.roleId="$${OP_KMS_ROLE_ID}"

deploy-pr-env-deps:
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n clusters-service --query clientId -o tsv) && \
	oc process --local -f cspr/cluster-service-namespace.yaml \
		-p CLIENT_ID=$${AZURE_CS_MI_CLIENT_ID} \
		-p FPA_CLIENT_ID=${AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID} \
		-p ARM_HELPER_CLIENT_ID=${AZURE_ARM_HELPER_IDENTITY_CLIENT_ID} \
		-p ARM_HELPER_MOCK_FPA_PRINCIPAL_ID=${AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID} \
		-p MSI_MOCK_CLIENT_ID=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID} \
		-p MSI_MOCK_PRINCIPAL_ID=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID} | oc apply -f -
	oc process --local -f cspr/orphaned-namespace-cleaner.yaml | oc apply -f -

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

local-deploy-provision-shard:
	@ZONE_RESOURCE_ID=$(shell az network dns zone show -n ${ZONE_NAME} -g ${REGIONAL_RESOURCEGROUP} --query id -o tsv) && \
	CX_SECRETS_KV_URL=$(shell az keyvault show -n ${CX_SECRETS_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	CX_MI_KV_URL=$(shell az keyvault show -n ${CX_MI_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) local/provisioning-shards.tmpl.yml local/local-provisioning-shards.yml -e zoneResourceId=$${ZONE_RESOURCE_ID},cxSecretsKeyVaultUrl=$${CX_SECRETS_KV_URL},cxMiKeyVaultUrl=$${CX_MI_KV_URL},maestroRestUrl=http://localhost:8001,maestroGrpUrl=localhost:8090
	@cat local/local-provisioning-shards.yml

personal-runtime-config:
	@TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.web -o tsv) && \
	OCP_ACR_URL=$(shell az acr show -n ${OCP_ACR_NAME} --query loginServer -o tsv) && \
	OCP_ACR_RESOURCE_ID=$(shell az acr show -n ${OCP_ACR_NAME} --query id -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) local/azure-runtime-config.tmpl.yaml local/azure-runtime-config.yaml \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT}
	@cat local/azure-runtime-config.yaml
.PHONY: personal-runtime-config

local-azure-operators-managed-identities-config:
	@OP_CLOUD_CONTROLLER_MANAGER_ROLE_ID=$(shell az role definition list --name "${OP_CLOUD_CONTROLLER_MANAGER_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_INGRESS_ROLE_ID=$(shell az role definition list --name "${OP_INGRESS_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_DISK_CSI_DRIVER_ROLE_ID=$(shell az role definition list --name "${OP_DISK_CSI_DRIVER_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_FILE_CSI_DRIVER_ROLE_ID=$(shell az role definition list --name "${OP_FILE_CSI_DRIVER_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_CLUSTER_API_AZURE_ROLE_ID=$(shell az role definition list --name "${OP_CLUSTER_API_AZURE_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_CONTROL_PLANE_ROLE_ID=$(shell az role definition list --name "${OP_CONTROL_PLANE_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_IMAGE_REGISTRY_DRIVER_ROLE_ID=$(shell az role definition list --name "${OP_IMAGE_REGISTRY_DRIVER_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_CLOUD_NETWORK_CONFIG_ROLE_ID=$(shell az role definition list --name "${OP_CLOUD_NETWORK_CONFIG_ROLE_NAME}" --query "[].name" -o tsv) && \
	OP_KMS_ROLE_ID=$(shell az role definition list --name "${OP_KMS_ROLE_NAME}" --query "[].name" -o tsv) && \
	helm template deploy -s templates/azure-operators-managed-identities-config.configmap.yaml \
	  --set azureOperatorsMI.cloudControllerManager.roleName="${OP_CLOUD_CONTROLLER_MANAGER_ROLE_NAME}" \
	  --set azureOperatorsMI.cloudControllerManager.roleId="$${OP_CLOUD_CONTROLLER_MANAGER_ROLE_ID}" \
	  --set azureOperatorsMI.ingress.roleName="${OP_INGRESS_ROLE_NAME}" \
	  --set azureOperatorsMI.ingress.roleId="$${OP_INGRESS_ROLE_ID}" \
	  --set azureOperatorsMI.clusterApiAzure.roleName="${OP_CLUSTER_API_AZURE_ROLE_NAME}" \
	  --set azureOperatorsMI.clusterApiAzure.roleId="$${OP_CLUSTER_API_AZURE_ROLE_ID}" \
	  --set azureOperatorsMI.controlPlane.roleName="${OP_CONTROL_PLANE_ROLE_NAME}" \
	  --set azureOperatorsMI.controlPlane.roleId="$${OP_CONTROL_PLANE_ROLE_ID}" \
	  --set azureOperatorsMI.diskCsiDriver.roleName="${OP_DISK_CSI_DRIVER_ROLE_NAME}" \
	  --set azureOperatorsMI.diskCsiDriver.roleId="$${OP_DISK_CSI_DRIVER_ROLE_ID}" \
	  --set azureOperatorsMI.fileCsiDriver.roleName="${OP_FILE_CSI_DRIVER_ROLE_NAME}" \
	  --set azureOperatorsMI.fileCsiDriver.roleId="$${OP_FILE_CSI_DRIVER_ROLE_ID}" \
	  --set azureOperatorsMI.imageRegistry.roleName="${OP_IMAGE_REGISTRY_DRIVER_ROLE_NAME}" \
	  --set azureOperatorsMI.imageRegistry.roleId="$${OP_IMAGE_REGISTRY_DRIVER_ROLE_ID}" \
	  --set azureOperatorsMI.cloudNetworkConfig.roleName="${OP_CLOUD_NETWORK_CONFIG_ROLE_NAME}" \
	  --set azureOperatorsMI.cloudNetworkConfig.roleId="$${OP_CLOUD_NETWORK_CONFIG_ROLE_ID}" \
	  --set azureOperatorsMI.kms.roleName="${OP_KMS_ROLE_NAME}" \
	  --set azureOperatorsMI.kms.roleId="$${OP_KMS_ROLE_ID}" \
	  | yq '.data."azure-operators-managed-identities-config.yaml"' > ./azure-operators-managed-identities-config.yaml
.PHONY: local-azure-operators-managed-identities-config

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
	../templatize.sh $(DEPLOY_ENV) local/azure-runtime-config.tmpl.yaml local/azure-runtime-config.yaml \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT}
	@cat local/azure-runtime-config.yaml
.PHONY: shared-storage-runtime-config

.PHONY: create-pr-env-sp deploy deploy-using-azure-db deploy-integ configure-tmp-provision-shard deploy-secrets-template deploy-secrets-template-using-azure-db deploy-istio-configurations-template deploy-namespace-template runtime-config
