-include ../setup-env.mk
-include ../helm-cmd.mk

deploy:
	@kubectl create namespace ${HYPERSHIFT_NAMESPACE} --dry-run=client -o json | kubectl apply -f - && \
	AZURE_TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	AZURE_SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) && \
	CSI_SECRET_STORE_CLIENT_ID=$(shell az aks show -n ${AKS_NAME} -g ${RESOURCEGROUP} --query 'addonProfiles.azureKeyvaultSecretsProvider.identity.clientId' -o tsv) && \
	${HELM_CMD} hypershift deploy \
		--namespace ${HYPERSHIFT_NAMESPACE} \
		--set image=${ARO_HCP_SVC_ACR}.azurecr.io/${HO_IMAGE_REPOSITORY} \
		--set imageDigest=${HO_IMAGE_DIGEST} \
		--set registryOverrides="quay.io/openshift-release-dev/ocp-v4.0-art-dev=${ARO_HCP_OCP_ACR}.azurecr.io/openshift/release\,quay.io/openshift-release-dev/ocp-release=${ARO_HCP_OCP_ACR}.azurecr.io/openshift/release-images\,registry.redhat.io/redhat=${ARO_HCP_OCP_ACR}.azurecr.io/redhat" \
		--set additionalArgs="${HO_ADDITIONAL_INSTALL_ARG}" \
		--set azureKeyVaultClientId=$${CSI_SECRET_STORE_CLIENT_ID}

.PHONY: deploy
