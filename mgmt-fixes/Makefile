-include ../setup-env.mk
-include ../helm-cmd.mk
HELM_CMD ?= helm upgrade --install

deploy:
	${HELM_CMD} mgmt-fixes \
		deploy/helm/mgmt-fixes/
.PHONY: deploy

undeploy:
	helm uninstall mgmt-fixes
.PHONY: undeploy
