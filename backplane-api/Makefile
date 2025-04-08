-include ../setup-env.mk
-include ../helm-cmd.mk

deploy:
	@kubectl create namespace aro-hcp --dry-run=client -o json | kubectl apply -f - && \
	AZURE_TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	BACKPLANE_MI_CLIENT_ID=$$(az identity show -g ${RESOURCEGROUP} -n backplane --query clientId -o tsv) && \
	BACKPLANE_MI_TENANT_ID=$$(az identity show -g ${RESOURCEGROUP} -n backplane --query tenantId -o tsv) && \
	IMAGE_PULLER_MI_CLIENT_ID=$$(az identity show -g ${RESOURCEGROUP} -n image-puller --query clientId -o tsv) && \
	IMAGE_PULLER_MI_TENANT_ID=$$(az identity show -g ${RESOURCEGROUP} -n image-puller --query tenantId -o tsv) && \
	CSI_SECRET_STORE_CLIENT_ID=$(shell az aks show -g ${RESOURCEGROUP} -n ${AKS_NAME} --query addonProfiles.azureKeyvaultSecretsProvider.identity.clientId -o tsv) && \
	IMAGE_TAG=$$(../get-tag.sh ${ARO_HCP_IMAGE_ACR} backplane-api) && \
	${HELM_CMD} aro-hcp-backplane-api-dev deploy/helm/backplane-api/ \
		--namespace aro-hcp \
		--set azure.clientId=$${CSI_SECRET_STORE_CLIENT_ID} \
		--set azure.tenantId=$${AZURE_TENANT_ID} \
		--set serviceAccount.name="backplane-api" \
		--set serviceAccount.workloadIdentityClientId=$${BACKPLANE_MI_CLIENT_ID} \
		--set serviceAccount.workloadIdentityTenantId=$${BACKPLANE_MI_TENANT_ID} \
		--set pullBinding.workloadIdentityClientId=$${IMAGE_PULLER_MI_CLIENT_ID} \
		--set pullBinding.workloadIdentityTenantId=$${IMAGE_PULLER_MI_TENANT_ID} \
		--set pullBinding.registry=${ARO_HCP_IMAGE_ACR}.azurecr.io \
		--set pullBinding.scope="repository:backplane-api:pull" \
		--set configMap.currentVersion=${ARO_HCP_IMAGE_ACR}.azurecr.io/backplane-api:$${IMAGE_TAG} \
		--set statefulset.imageName=${ARO_HCP_IMAGE_ACR}.azurecr.io/backplane-api:$${IMAGE_TAG} \
		--set statefulset.replicaCount=3 \
		--set configMap.ocmUrl="https://api.stage.openshift.com" \
		--set secrets.authSecretName="ocm" \
		--set secrets.tlsSecretName="backplane-api-tls" \
		--set istio.enabled=true \
		--set istio.ingressNamespace="aks-istio-ingress" \
		--set istio.ingressPort="8443"

.PHONY: deploy