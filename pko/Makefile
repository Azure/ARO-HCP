-include ../setup-env.mk
-include ../helm-cmd.mk

NAMESPACE ?= package-operator-system
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_IMAGE_REPOSITORY ?= package-operator/package-operator-package

deploy:
	@kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f -
	IMAGE_PULLER_MI_CLIENT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n image-puller \
			--query clientId -o tsv) && \
	IMAGE_PULLER_MI_TENANT_ID=$$(az identity show \
			-g ${RESOURCEGROUP} \
			-n image-puller \
			--query tenantId -o tsv) && \
	${HELM_CMD} package-operator ./deploy \
	--namespace ${NAMESPACE} \
	--set pkoImage=${PKO_IMAGE} \
	--set pkoImageManager=${PKO_IMAGE_MANAGER} \
	--set pkoImageTag=${PKO_IMAGE_TAG} \
	--set pullBinding.workloadIdentityClientId="$${IMAGE_PULLER_MI_CLIENT_ID}" \
	--set pullBinding.workloadIdentityTenantId="$${IMAGE_PULLER_MI_TENANT_ID}" \
	--set pullBinding.registry=${ARO_HCP_IMAGE_REGISTRY} \
	--set pullBinding.scope='repository:*:pull'

image:
	az acr login --name ${ARO_HCP_IMAGE_ACR} && \
	cd $$(mktemp -d) && \
	git clone https://github.com/package-operator/package-operator.git && \
	cd package-operator && \
	git checkout ${PKO_IMAGE_TAG} && \
	IMAGE_REGISTRY=${ARO_HCP_IMAGE_REGISTRY}/package-operator ./do ci:release

.PHONY: deploy
