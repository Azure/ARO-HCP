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
	--set pullBinding.workloadIdentityClientId="$${IMAGE_PULLER_MI_CLIENT_ID}" \
	--set pullBinding.workloadIdentityTenantId="$${IMAGE_PULLER_MI_TENANT_ID}" \
	--set pullBinding.registry=${ARO_HCP_IMAGE_REGISTRY} \
	--set pullBinding.scope='repository:*:pull' \
	--set imagePrefixOverrides=${PKO_IMAGE_PREFIX_OVERRIDES} \
	--set pkoImagePackageRegistry=${PKO_IMAGEPACKAGE_REGISTRY} \
	--set pkoImagePackageRepository=${PKO_IMAGEPACKAGE_REPOSITORY} \
	--set pkoImagePackageDigest=${PKO_IMAGEPACKAGE_DIGEST} \
	--set pkoImageManagerRegistry=${PKO_IMAGEMANAGER_REGISTRY} \
	--set pkoImageManagerRepository=${PKO_IMAGEMANAGER_REPOSITORY} \
	--set pkoImageManagerDigest=${PKO_IMAGEMANAGER_DIGEST}

.PHONY: deploy
