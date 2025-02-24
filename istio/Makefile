-include ../setup-env.mk
-include ../helm-cmd.mk
HELM_CMD ?= helm upgrade --install

deploy:
ifeq ($(DEPLOY_ENV), personal-dev)
	@echo "Skipping Istio deployment: Running in personal-dev environment."
else
	${HELM_CMD} istio \
		deploy/helm/istio/ \
		--namespace aks-istio-system
endif
.PHONY: deploy

undeploy:
	helm uninstall istio
.PHONY: undeploy
