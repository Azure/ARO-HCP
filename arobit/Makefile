-include ../setup-env.mk
-include ../helm-cmd.mk

deploy:
	kubectl create namespace mds --dry-run=client -o json | kubectl apply -f - && \
	${HELM_CMD} arobit \
		deploy/ \
		--namespace mds
.PHONY: deploy

undeploy:
	helm uninstall arobit --namespace mds
.PHONY: undeploy
