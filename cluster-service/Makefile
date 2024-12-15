SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk

deploy: provision-shard
	ISTO_VERSION=$(shell az aks show -n ${AKS_NAME} -g ${RESOURCEGROUP} --query serviceMeshProfile.istio.revisions[-1] -o tsv) && \
	kubectl create namespace cluster-service --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace cluster-service "istio.io/rev=$${ISTO_VERSION}" --overwrite=true && \
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n clusters-service --query clientId -o tsv) && \
	CS_SERVICE_PRINCIPAL_CREDS_BASE64='$(shell az keyvault secret show --vault-name "${SERVICE_KV}" --name "aro-hcp-dev-sp-cs" | jq .value -r | base64 | tr -d '\n')' && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.web -o tsv) && \
	OCP_ACR_URL=$(shell az acr show -n ${OCP_ACR_NAME} --query loginServer -o tsv) && \
	OCP_ACR_RESOURCE_ID=$(shell az acr show -n ${OCP_ACR_NAME} --query id -o tsv) && \
	helm upgrade --install cluster-service --namespace cluster-service \
	  deploy/helm/ \
	  --set azureCsMiClientId=$${AZURE_CS_MI_CLIENT_ID} \
	  --set oidcIssuerBlobServiceUrl=$${OIDC_BLOB_SERVICE_ENDPOINT} \
	  --set oidcIssuerBaseUrl=$${OIDC_ISSUER_BASE_ENDPOINT} \
	  --set tenantId=$${TENANT_ID} \
	  --set region=${REGION} \
	  --set serviceKeyvaultName=${SERVICE_KV} \
	  --set csServicePrincipalCredsBase64=$${CS_SERVICE_PRINCIPAL_CREDS_BASE64} \
	  --set imageRegistry=${ACR_NAME}.azurecr.io \
	  --set imageRepository=${IMAGE_REPO} \
	  --set imageTag=${IMAGE_TAG} \
	  --set azureFirstPartyApplicationClientId=${AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID} \
	  --set fpaCertName=${FPA_CERT_NAME} \
	  --set ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID} \
	  --set ocpAcrUrl=$${OCP_ACR_URL} \
	  --set databaseDisableTls=${DATABASE_DISABLE_TLS} \
	  --set databaseAuthMethod=${DATABASE_AUTH_METHOD} \
	  --set provisionShardsConfig="$(shell base64 -i deploy/provisioning-shards.yml | tr -d '\n')" \
	  --set deployLocalDatabase=${DEPLOY_LOCAL_DB} \
	  --set databaseHost=${DB_HOST} \
	  --set databaseName=${DB_NAME} \
	  --set databaseUser=${DB_USERNAME} \
	  --set databasePassword=${DB_PASSWORD} \
	  --set azureMiMockServicePrincipalPrincipalId=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID} \
	  --set azureMiMockServicePrincipalClientId=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID} \
	  --set azureMiMockServicePrincipalCertName=${MI_MOCK_SERVICE_PRINCIPAL_CERT_NAME} \
	  --set azureArmHelperIdentityCertName=${ARM_HELPER_CERT_NAME} \
	  --set azureArmHelperIdentityClientId=${AZURE_ARM_HELPER_IDENTITY_CLIENT_ID} \
	  --set azureArmHelperMockFpaPrincipalId=${AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID} \
	  --set azureOperatorsManagedIdentitiesConfig=${AZURE_OPERATORS_MANAGED_IDENTITIES_CONFIG}

deploy-pr-env-deps:
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n clusters-service --query clientId -o tsv) && \
	oc process --local -f deploy/integration/cluster-service-namespace.yaml \
		-p CLIENT_ID=$${AZURE_CS_MI_CLIENT_ID} | oc apply -f -

create-pr-env-sp:
	CLUSTER_ID=$(shell az aks show -g ${RESOURCEGROUP} -n aro-hcp-aks --query id -o tsv) && \
	az ad sp create-for-rbac \
	--display-name "cs-pr-authentication" \
	--role 'Azure Kubernetes Service RBAC Admin' \
	--scopes "$${CLUSTER_ID}"

provision-shard:
	@ZONE_RESOURCE_ID=$(shell az network dns zone show -n ${ZONE_NAME} -g ${REGIONAL_RESOURCEGROUP} --query id -o tsv) && \
	CX_SECRETS_KV_URL=$(shell az keyvault show -n ${CX_SECRETS_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	CX_MI_KV_URL=$(shell az keyvault show -n ${CX_MI_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) deploy/provisioning-shards.tmpl.yml deploy/provisioning-shards.yml -e zoneResourceId=$${ZONE_RESOURCE_ID},cxSecretsKeyVaultUrl=$${CX_SECRETS_KV_URL},cxMiKeyVaultUrl=$${CX_MI_KV_URL},maestroRestUrl=http://maestro.maestro.svc.cluster.local:8000,maestroGrpUrl=maestro-grpc.maestro.svc.cluster.local:8090
	@cat deploy/provisioning-shards.yml

local-deploy-provision-shard:
	@ZONE_RESOURCE_ID=$(shell az network dns zone show -n ${ZONE_NAME} -g ${REGIONAL_RESOURCEGROUP} --query id -o tsv) && \
	CX_SECRETS_KV_URL=$(shell az keyvault show -n ${CX_SECRETS_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	CX_MI_KV_URL=$(shell az keyvault show -n ${CX_MI_KV_NAME} -g ${MGMT_RESOURCEGROUP} --query properties.vaultUri -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) deploy/provisioning-shards.tmpl.yml deploy/local-provisioning-shards.yml -e zoneResourceId=$${ZONE_RESOURCE_ID},cxSecretsKeyVaultUrl=$${CX_SECRETS_KV_URL},cxMiKeyVaultUrl=$${CX_MI_KV_URL},maestroRestUrl=http://localhost:8001,maestroGrpUrl=localhost:8090
	@cat deploy/local-provisioning-shards.yml

personal-runtime-config:
	@TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.web -o tsv) && \
	OCP_ACR_URL=$(shell az acr show -n ${OCP_ACR_NAME} --query loginServer -o tsv) && \
	OCP_ACR_RESOURCE_ID=$(shell az acr show -n ${OCP_ACR_NAME} --query id -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) deploy/azure-runtime-config.tmpl.yaml deploy/azure-runtime-config.yaml \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT}
	@cat deploy/azure-runtime-config.yaml
.PHONY: personal-runtime-config

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
			publicBlobAccess=true \
			aroDevopsMsiId=${DEVOPS_MSI_ID} \
			deploymentScriptLocation=westus3
.PHONY: shared-oidc-storage

shared-storage-runtime-config:
	@TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${SHARED_OIDC_STORAGE_ACCOUNT_NAME} -g ${SHARED_OIDC_RG_NAME} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell az storage account show -n ${SHARED_OIDC_STORAGE_ACCOUNT_NAME} -g ${SHARED_OIDC_RG_NAME} --query primaryEndpoints.web -o tsv) && \
	OCP_ACR_URL=$(shell az acr show -n ${OCP_ACR_NAME} --query loginServer -o tsv) && \
	OCP_ACR_RESOURCE_ID=$(shell az acr show -n ${OCP_ACR_NAME} --query id -o tsv) && \
	../templatize.sh $(DEPLOY_ENV) deploy/azure-runtime-config.tmpl.yaml deploy/azure-runtime-config.yaml \
		-e tenantId=$${TENANT_ID},ocpAcrResourceId=$${OCP_ACR_RESOURCE_ID},ocpAcrResourceUrl=$${OCP_ACR_URL},oidc_container='$$web',oidc_issuer_base_url=$${OIDC_ISSUER_BASE_ENDPOINT},oidc_service_url=$${OIDC_BLOB_SERVICE_ENDPOINT}
	@cat deploy/azure-runtime-config.yaml
.PHONY: shared-storage-runtime-config

.PHONY: create-pr-env-sp deploy deploy-using-azure-db deploy-integ provision-shard configure-tmp-provision-shard deploy-secrets-template deploy-secrets-template-using-azure-db deploy-istio-configurations-template deploy-namespace-template runtime-config
