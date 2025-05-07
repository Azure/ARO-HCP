-include ../setup-env.mk
-include ../helm-cmd.mk

deploy: pull-chart
	@kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f -
	$(eval ESO_DIGEST := $(subst sha256:,,$(ESO_DIGEST)))
	${HELM_CMD} eso external-secrets/external-secrets \
		--version ${CHART_VERSION} \
		--namespace ${NAMESPACE} \
		--set serviceAccount.name=${SERVICE_ACCOUNT} \
		--set installCRDs=true \
		--set image.repository=${ACR_NAME}.azurecr.io/${ESO_REPOSTIORY}@sha256 \
		--set image.tag=${ESO_DIGEST} \
		--set webhook.image.repository=${ACR_NAME}.azurecr.io/${ESO_REPOSTIORY}@sha256 \
		--set webhook.image.tag=${ESO_DIGEST} \
		--set certController.image.repository=${ACR_NAME}.azurecr.io/${ESO_REPOSTIORY}@sha256 \
		--set certController.image.tag=${ESO_DIGEST}
.PHONY: deploy

pull-chart:
    # https://github.com/external-secrets/external-secrets/tree/main/deploy/charts/external-secrets
	helm repo add external-secrets https://charts.external-secrets.io
	helm repo update
.PHONY: pull-chart
