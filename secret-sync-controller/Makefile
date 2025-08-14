-include ../setup-env.mk

NAMESPACE ?= secret-sync-controller

deploy:
	$(eval IMAGE_DIGEST := $(subst sha256:,,$(IMAGE_DIGEST)))
	kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f - && \
	KUBE_VERSION=$(shell kubectl version --output=json | jq -r '.serverVersion.gitVersion' | sed 's/^v//') && \
	../hack/helm.sh secret-sync-controller deploy ${NAMESPACE} \
		-f deploy/values-azure.yaml \
		--set image.repository=${ACR_NAME}.azurecr.io/${IMAGE_REPOSITORY}@sha256 \
		--set image.tag=${IMAGE_DIGEST} \
		--set providerContainer[0].image=${PROVIDER_IMAGE} \
		--set validatingAdmissionPolicies.kubernetesReleaseVersion=$${KUBE_VERSION}
.PHONY: deploy
