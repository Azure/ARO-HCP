-include ../setup-env.mk

deploy:
	../hack/helm.sh istio deploy aks-istio-system \
		--set "istioVersions={${ISTIO_VERSIONS}}"
.PHONY: deploy

undeploy:
	helm uninstall istio
.PHONY: undeploy
