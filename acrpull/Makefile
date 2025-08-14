-include ../setup-env.mk

deploy:
	kubectl create namespace acrpull --dry-run=client -o json | kubectl apply -f - && \
	../hack/helm.sh acrpull deploy acrpull \
		--set image=${ACRPULL_REGISTRY}/${ACRPULL_REPO}@${ACRPULL_DIGEST}
.PHONY: deploy

undeploy:
	helm uninstall acrpull --namespace acrpull
.PHONY: undeploy
