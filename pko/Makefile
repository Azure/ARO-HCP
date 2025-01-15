-include ../setup-env.mk
-include ../helm-cmd.mk
HELM_CMD ?= helm upgrade --install

NAMESPACE ?= package-operator-system

deploy:
	@kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f -
	${HELM_CMD} package-operator ./helm \
	--namespace ${NAMESPACE} \
	--set pkoImage=${PKO_IMAGE} \
	--set pkoImageManager=${PKO_IMAGE_MANAGER} \
	--set pkoImageTag=${PKO_IMAGE_TAG}

.PHONY: deploy
