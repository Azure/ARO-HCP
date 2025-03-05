-include ../setup-env.mk
-include ../helm-cmd.mk

deploy:
	${HELM_CMD} istio \
		deploy/ \
		--set "istioVersions={${ISTIO_VERSIONS}}" \
		--namespace aks-istio-system
.PHONY: deploy

undeploy:
	helm uninstall istio
.PHONY: undeploy
