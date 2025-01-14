-include ../setup-env.mk
-include ../helm-cmd.mk
HELM_CMD ?= helm upgrade --install

deploy:
	${HELM_CMD} mgmt-fixes \
		deploy/helm/mgmt-fixes/
#	kubectl create namespace mgmt-fixes --dry-run=client -o json | kubectl apply -f - && \
#		--namespace mgmt-fixes
.PHONY: deploy

undeploy:
	helm uninstall mgmt-fixes
.PHONY: undeploy
