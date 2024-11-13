SHELL := /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk

deploy:
	@EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n ${EXTERNAL_DNS_MI_NAME} --query clientId -o tsv) && \
	AZURE_TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	AZURE_SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) && \
	CSI_SECRET_STORE_CLIENT_ID=$(shell az aks show -n ${AKS_NAME} -g ${RESOURCEGROUP} --query 'addonProfiles.azureKeyvaultSecretsProvider.identity.clientId' -o tsv) && \
	helm upgrade --install hypershift deploy/helm \
		--create-namespace --namespace ${HYPERSHIFT_NAMESPACE} \
		--set image=${HO_IMAGE_BASE} \
		--set imageTag=${HO_IMAGE_TAG} \
		--set registryOverrides="quay.io/openshift-release-dev/ocp-v4.0-art-dev=${ARO_HCP_OCP_ACR}.azurecr.io/openshift/release\,quay.io/openshift-release-dev/ocp-release=${ARO_HCP_OCP_ACR}.azurecr.io/openshift/release-images\,registry.redhat.io/redhat=${ARO_HCP_OCP_ACR}.azurecr.io/redhat" \
		--set additionalArgs="${HO_ADDITIONAL_INSTALL_ARG}" \
		--set azureKeyVaultClientId=$${CSI_SECRET_STORE_CLIENT_ID} \
		--set external-dns.image=${ED_IMAGE_BASE} \
		--set external-dns.imageTag=${ED_IMAGE_TAG} \
		--set external-dns.txtOwnerId=${RESOURCEGROUP} \
		--set external-dns.domain=${ZONE_NAME} \
		--set external-dns.credentials.tenantId=$${AZURE_TENANT_ID} \
		--set external-dns.credentials.subscriptionId=$${AZURE_SUBSCRIPTION_ID} \
		--set external-dns.credentials.resourceGroup=${REGIONAL_RESOURCEGROUP} \
		--set external-dns.credentials.userAssignedIdentityID=$${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID}

.PHONY: helm-chart deploy
