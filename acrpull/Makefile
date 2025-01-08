-include ../setup-env.mk
-include ../helm-cmd.mk
HELM_CMD ?= helm upgrade --install

deploy:
	kubectl create namespace acrpull --dry-run=client -o json | kubectl apply -f - && \
	${HELM_CMD} acrpull \
		deploy/helm/acrpull/ \
		--set image=mcr.microsoft.com/aks/msi-acrpull@${ACRPULL_DIGEST} \
		--namespace acrpull
.PHONY: deploy

undeploy:
	helm uninstall acrpull --namespace acrpull
.PHONY: undeploy
