-include ../setup-env.mk

deploy:
	kubectl create namespace acrpull --dry-run=client -o json | kubectl apply -f - && \
	helm upgrade --install ${HELM_DRY_RUN} acrpull \
		deploy/helm/acrpull/ \
		--set image=mcr.microsoft.com/aks/msi-acrpull@${ACRPULL_DIGEST} \
		--namespace acrpull
.PHONY: deploy

undeploy:
	helm uninstall acrpull --namespace acrpull
.PHONY: undeploy
