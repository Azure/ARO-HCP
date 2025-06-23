

-include ../setup-env.mk
-include ../helm-cmd.mk

deploy:
	kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f - && \
	$(eval IMAGE_DIGEST := $(subst sha256:,,$(IMAGE_DIGEST)))
	${HELM_CMD} secret-sync-controller \
		deploy/ \
		--set image.repository=${ACR_NAME}.azurecr.io/${IMAGE_REPOSITORY}@sha256 \
		--set image.tag=${IMAGE_DIGEST} \
		--namespace ${NAMESPACE}
.PHONY: deploy
