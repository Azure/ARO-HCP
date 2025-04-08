-include ../setup-env.mk
-include ../helm-cmd.mk
HELM_CMD ?= helm upgrade --install

CURRENT_COMMIT := $(shell git rev-parse --short=7 HEAD)
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_IMAGE_REPOSITORY ?= backplane-api
ARO_HCP_BACKPLANE_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(ARO_HCP_IMAGE_REPOSITORY)

info:
	@echo "ARO_HCP_BACKPLANE_IMAGE: ${ARO_HCP_BACKPLANE_IMAGE}"
	@echo "COMMIT: ${CURRENT_COMMIT}"

clean:
	rm -f backplane-api
.PHONY: clean

image:
	pushd .. && git archive --output backplane-api/archive.tar.gz HEAD && popd
	docker build -f "./Dockerfile" -t ${ARO_HCP_BACKPLANE_IMAGE}:${CURRENT_COMMIT} .
	rm -f archive.tar.gz
.PHONY: image

push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${ARO_HCP_BACKPLANE_IMAGE}:${CURRENT_COMMIT}
.PHONY: push

deploy:
	IMAGE_TAG=$$(../get-tag.sh ${ARO_HCP_IMAGE_ACR} backplane-api) && \
	BACKPLANE_MI_CLIENT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n backplane \
			--query clientId -o tsv) && \
	BACKPLANE_MI_TENANT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n backplane \
			--query tenantId -o tsv) && \
	IMAGE_PULLER_MI_CLIENT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n image-puller \
			--query clientId -o tsv) && \
	IMAGE_PULLER_MI_TENANT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n image-puller \
			--query tenantId -o tsv) && \
	SECRET_STORE_MI_CLIENT_ID=$$(az aks show --resource-group ${RESOURCEGROUP} \
			--name ${AKS_NAME} \
			--query addonProfiles.azureKeyvaultSecretsProvider.identity.clientId \
			--output tsv) && \
	TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	OCM_URL="https://api.stage.openshift.com" && \
	kubectl create namespace aro-hcp --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace aro-hcp "istio.io/rev=${ISTO_TAG}" --overwrite=true && \
	${HELM_CMD} aro-hcp-backplane-api-dev \
		deploy/helm/backplane-api/ \
		--set azure.clientId="$${SECRET_STORE_MI_CLIENT_ID}" \
		--set azure.tenantId="$${TENANT_ID}" \
		--set serviceAccount.name="backplane-api" \
		--set serviceAccount.workloadIdentityClientId="$${BACKPLANE_MI_CLIENT_ID}" \
		--set serviceAccount.workloadIdentityTenantId="$${BACKPLANE_MI_TENANT_ID}" \
		--set pullBinding.workloadIdentityClientId="$${IMAGE_PULLER_MI_CLIENT_ID}" \
		--set pullBinding.workloadIdentityTenantId="$${IMAGE_PULLER_MI_TENANT_ID}" \
		--set pullBinding.registry=${ARO_HCP_IMAGE_REGISTRY} \
		--set pullBinding.scope=repository:${ARO_HCP_IMAGE_REPOSITORY}:pull \
		--set configMap.currentVersion=${ARO_HCP_BACKPLANE_IMAGE}:$${IMAGE_TAG} \
		--set statefulset.imageName=${ARO_HCP_BACKPLANE_IMAGE}:$${IMAGE_TAG} \
		--set statefulset.replicaCount=3 \
		--set configMap.ocmUrl="$${OCM_URL}" \
		--set secrets.authSecretName="ocm" \
		--set secrets.tlsSecretName="backplane-api-tls" \
		--set istio.enabled=true \
		--set istio.ingressNamespace="aks-istio-ingress" \
		--set istio.ingressPort="8443" \
		--namespace aro-hcp
.PHONY: deploy

undeploy:
	helm uninstall aro-hcp-backplane-api-dev --namespace aro-hcp
.PHONY: undeploy
