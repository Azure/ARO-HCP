-include ../setup-templatize-env.mk

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

local-deploy-provision-shard:
	@ZONE_RESOURCE_ID=$(shell az network dns zone show -n ${ZONE_NAME} -g ${REGIONAL_RESOURCEGROUP} --query id -o tsv) && \
	CX_SECRETS_KV_URL=$(shell az keyvault show -n ${CX_SECRETS_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	CX_MI_KV_URL=$(shell az keyvault show -n ${CX_MI_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	CX_SECRETS_KV_MI_CLIENT_ID=$(shell az aks show -n ${MGMT_AKS_NAME} -g ${MGMT_RESOURCEGROUP} --query 'addonProfiles.azureKeyvaultSecretsProvider.identity.clientId' -o tsv) && \
	AKS_MGMT_CLUSTER_RESOURCE_ID=$(shell az aks show -n ${MGMT_AKS_NAME} -g ${MGMT_RESOURCEGROUP} --query id -o tsv) && \
	if [ -z "$${CX_SECRETS_KV_MI_CLIENT_ID}" ]; then echo "Failed to get CX_SECRETS_KV_MI_CLIENT_ID - make sure to provision a MGMT cluster first" >&2; exit 1; fi && \
	../templatize.sh $(DEPLOY_ENV) local/provisioning-shards.tmpl.yml local/local-provisioning-shards.yml -e zoneResourceId=$${ZONE_RESOURCE_ID},cxSecretsKeyVaultUrl=$${CX_SECRETS_KV_URL},cxMiKeyVaultUrl=$${CX_MI_KV_URL},cxSecretsKeyVaultMiClientId=$${CX_SECRETS_KV_MI_CLIENT_ID},maestroRestUrl=http://localhost:8001,maestroGrpUrl=localhost:8090,shardId=$(PROVISION_SHARD_ID),aksManagementClusterResourceId=$${AKS_MGMT_CLUSTER_RESOURCE_ID}
	@cat local/local-provisioning-shards.yml

personal-runtime-config:
	@TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${REGIONAL_RESOURCEGROUP} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell ./oidc-base-endpoint.sh ${OIDC_STORAGE_ACCOUNT} ${REGIONAL_RESOURCEGROUP} ${AFD_OIDC_BASE_ENDPOINT}) && \
	OCP_ACR_URL=$(shell az acr show -n ${OCP_ACR_NAME} --query loginServer -o tsv) && \
	OCP_ACR_RESOURCE_ID=$(shell az acr show -n ${OCP_ACR_NAME} --query id -o tsv) && \
	COSMOS_DB_URL=$(shell az cosmosdb show -n ${COSMOS_DB_NAME} -g ${REGIONAL_RESOURCEGROUP} --query documentEndpoint -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) local/azure-runtime-config.tmpl.json local/azure-runtime-config.json \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT},tls_certificates_issuer=$${TLS_CERTIFICATES_ISSUER},tls_certificates_enabled=$(TLS_CERTIFICATES_ENABLED),azureCloudEnvironmentName=$(AZURE_CLOUD_ENVIRONMENT_NAME),cosmosDatabaseUrl=$${COSMOS_DB_URL}
	@cat local/azure-runtime-config.json | jq
.PHONY: personal-runtime-config

local-azure-operators-managed-identities-config:
	helm template deploy -s templates/azure-operators-managed-identities-config.configmap.yaml \
	  --set azureOperatorsMI.roleSetName=${OPERATOR_ROLE_SET_NAME} \
	  | yq '.data["azure-operators-managed-identities-config.yaml"]' > local/azure-operators-managed-identities-config.yaml
.PHONY: local-azure-operators-managed-identities-config

local-aro-hcp-ocp-versions-config:
	helm template deploy \
	--set azureOperatorsMI.roleSetName=${OPERATOR_ROLE_SET_NAME} \
	-s templates/aro-hcp-ocp-versions-config.configmap.yaml | yq '.data["aro-hcp-ocp-versions-config.yaml"]' > local/aro-hcp-ocp-versions-config.yaml
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
	COSMOS_DB_URL=$(shell az cosmosdb show -n ${COSMOS_DB_NAME} -g ${REGIONAL_RESOURCEGROUP} --query documentEndpoint -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) local/azure-runtime-config.tmpl.json local/azure-runtime-config.json \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT},tls_certificates_issuer=$${TLS_CERTIFICATES_ISSUER},tls_certificates_enabled=$(TLS_CERTIFICATES_ENABLED),azureCloudEnvironmentName=$(AZURE_CLOUD_ENVIRONMENT_NAME),cosmosDatabaseUrl=$${COSMOS_DB_URL}
	@cat local/azure-runtime-config.json | jq
.PHONY: shared-storage-runtime-config

.PHONY: local-deploy-provision-shard
