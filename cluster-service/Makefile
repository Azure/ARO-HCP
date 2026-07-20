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

deploy-pr-env-deps:
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n clusters-service --query clientId -o tsv) && \
	oc process --local -f cspr/cluster-service-namespace.yaml \
		-p CLIENT_ID=$${AZURE_CS_MI_CLIENT_ID} \
		-p KEY_VAULT_NAME=${SERVICE_KV} \
		-p FIRST_PARTY_APP_CERTIFICATE_NAME=${FPA_CERT_NAME} \
		-p FPA_CLIENT_ID=${AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID} \
		-p ARM_HELPER_CERTIFICATE_NAME=${ARM_HELPER_CERT_NAME} \
		-p ARM_HELPER_CLIENT_ID=${AZURE_ARM_HELPER_IDENTITY_CLIENT_ID} \
		-p ARM_HELPER_MOCK_FPA_PRINCIPAL_ID=${AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID} \
		-p MSI_MOCK_CERTIFICATE_NAME=${MI_MOCK_SERVICE_PRINCIPAL_CERT_NAME} \
		-p MSI_MOCK_CLIENT_ID=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID} \
		-p MSI_MOCK_PRINCIPAL_ID=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID} | oc apply -f -
	oc process --local -f cspr/orphaned-namespace-cleaner.yaml | oc apply -f -
	az deployment group create \
		--name resource-cleaner-infra \
		--resource-group "${RESOURCEGROUP}" \
		--parameters ../dev-infrastructure/configurations/resource-cleaner.bicepparam
	CLEANER_OUTPUTS=$$(az deployment group show \
		--name resource-cleaner-infra \
		--resource-group "${RESOURCEGROUP}" \
		--query 'properties.outputs.{principalId:principalId.value,clientId:clientId.value,tenantId:tenantId.value}' -o json) && \
	CLEANER_PRINCIPAL_ID=$$(echo "$${CLEANER_OUTPUTS}" | jq -r '.principalId') && \
	az deployment sub create \
		--name "resource-cleaner-sub-${RESOURCEGROUP}" \
		--location "$$(az group show -n "${RESOURCEGROUP}" --query location -o tsv)" \
		--template-file ../dev-infrastructure/templates/dev-resource-cleaner-subscription.bicep \
		--parameters principalId="$${CLEANER_PRINCIPAL_ID}" && \
	AZURE_CLIENT_ID=$$(echo "$${CLEANER_OUTPUTS}" | jq -r '.clientId') \
	AZURE_TENANT_ID=$$(echo "$${CLEANER_OUTPUTS}" | jq -r '.tenantId') \
	./cspr/deploy-resource-cleaner.sh \
		--resource-group "${RESOURCEGROUP}"

cspr-jenkins-kubeconfig:
	./cspr-kubeconfig.sh cluster-service-admin cluster-service-mgmt ./cspr.kubeconfig

create-pr-env-sp:
	az deployment group create \
		--name cs-pr-auth-app \
		--resource-group "${RESOURCEGROUP}" \
		--parameters ../dev-infrastructure/configurations/cs-pr-auth-app.bicepparam
	@echo "NOTE: cs-pr-authentication may also need User Access Administrator on the AKS cluster."
	@echo "This requires elevated permissions and must be assigned manually if the SP is re-created."

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
	../templatize.sh $(DEPLOY_ENV) local/azure-runtime-config.tmpl.json local/azure-runtime-config.json \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT},tls_certificates_issuer=$${TLS_CERTIFICATES_ISSUER},tls_certificates_enabled=$(TLS_CERTIFICATES_ENABLED),azureCloudEnvironmentName=$(AZURE_CLOUD_ENVIRONMENT_NAME)
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
	../templatize.sh $(DEPLOY_ENV) local/azure-runtime-config.tmpl.json local/azure-runtime-config.json \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT},tls_certificates_issuer=$${TLS_CERTIFICATES_ISSUER},tls_certificates_enabled=$(TLS_CERTIFICATES_ENABLED),azureCloudEnvironmentName=$(AZURE_CLOUD_ENVIRONMENT_NAME)
	@cat local/azure-runtime-config.json | jq
.PHONY: shared-storage-runtime-config

.PHONY: create-pr-env-sp deploy-pr-env-deps cspr-jenkins-kubeconfig local-deploy-provision-shard
