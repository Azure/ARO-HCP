SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk

deploy: deploy-namespace-template deploy-istio-configurations-template ${DB_SECRET_TARGET}
	AZURE_CS_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n clusters-service --query clientId -o tsv) && \
	CS_SERVICE_PRINCIPAL_CREDS_BASE64='$(shell az keyvault secret show --vault-name "${SERVICE_KV}" --name "aro-hcp-dev-sp-cs" | jq .value -r | base64 | tr -d '\n')' && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.blob -o tsv) && \
	OIDC_ISSUER_BASE_ENDPOINT=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${RESOURCEGROUP} --query primaryEndpoints.web -o tsv) && \
	OCP_ACR_URL=$(shell az acr show -n ${OCP_ACR_NAME} --query loginServer -o tsv) && \
	OCP_ACR_RESOURCE_ID=$(shell az acr show -n ${OCP_ACR_NAME} --query id -o tsv) && \
	oc process --local -f deploy/openshift-templates/arohcp-service-template.yml \
	  -p AZURE_CS_MI_CLIENT_ID=$${AZURE_CS_MI_CLIENT_ID} \
	  -p TENANT_ID=$${TENANT_ID} \
	  -p REGION=${REGION} \
	  -p SERVICE_KEYVAULT_NAME=${SERVICE_KV} \
	  -p CS_SERVICE_PRINCIPAL_CREDS_BASE64=$${CS_SERVICE_PRINCIPAL_CREDS_BASE64} \
	  -p IMAGE_REGISTRY=${ACR_NAME}.azurecr.io \
	  -p IMAGE_REPOSITORY=${IMAGE_REPO} \
	  -p AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID=${AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID} \
	  -p FPA_CERT_NAME=${FPA_CERT_NAME} \
	  -p IMAGE_TAG=${IMAGE_TAG} \
	  -p OCP_ACR_RESOURCE_ID=$${OCP_ACR_RESOURCE_ID} \
	  -p OCP_ACR_URL=$${OCP_ACR_URL} \
	  -p DATABASE_DISABLE_TLS=${DATABASE_DISABLE_TLS} \
	  -p OIDC_ISSUER_BASE_URL=$${OIDC_ISSUER_BASE_ENDPOINT} \
	  -p OIDC_ISSUER_BLOB_SERVICE_URL=$${OIDC_BLOB_SERVICE_ENDPOINT} \
	  -p AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_PRINCIPAL_ID} \
	  -p AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID=${AZURE_MI_MOCK_SERVICE_PRINCIPAL_CLIENT_ID} \
	  -p AZURE_MI_MOCK_SERVICE_PRINCIPAL_CERT_NAME=${MI_MOCK_SERVICE_PRINCIPAL_CERT_NAME} \
	  -p DATABASE_AUTH_METHOD=${DATABASE_AUTH_METHOD} \
	  -p AZURE_ARM_HELPER_IDENTITY_CLIENT_ID=${AZURE_ARM_HELPER_IDENTITY_CLIENT_ID} \
	  -p AZURE_ARM_HELPER_IDENTITY_CERT_NAME=${ARM_HELPER_CERT_NAME} \
	  -p AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID=${AZURE_ARM_HELPER_MOCK_FPA_PRINCIPAL_ID} \
	  | oc apply -f -

deploy-namespace-template:
	ISTO_VERSION=$(shell az aks show -n ${AKS_NAME} -g ${RESOURCEGROUP} --query serviceMeshProfile.istio.revisions[-1] -o tsv) && \
	oc process --local -f deploy/openshift-templates/arohcp-namespace-template.yml \
		-p ISTIO_VERSION=$${ISTO_VERSION} | oc apply -f -

deploy-istio-configurations-template:
	kubectl apply -f deploy/istio.yml

deploy-local-db-secret: provision-shard
	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml \
		-p PROVISION_SHARDS_CONFIG="$$( base64 -i deploy/provisioning-shards.yml)" | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-db-template.yml | oc apply -f -

deploy-azure-db-secret: provision-shard
	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml \
		-p DATABASE_USER=clusters-service \
		-p DATABASE_NAME=clusters-service \
		-p DATABASE_PASSWORD="" \
		-p DATABASE_HOST=$(shell az postgres flexible-server show --resource-group ${RESOURCEGROUP} -n ${DATABASE_SERVER_NAME} --query fullyQualifiedDomainName -o tsv) \
		-p PROVISION_SHARDS_CONFIG="$$( base64 -i deploy/provisioning-shards.yml)" | oc apply -f -

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
