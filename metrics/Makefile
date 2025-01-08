-include ../setup-env.mk

deploy: deploy-config

deploy-config:
	kubectl apply ${KUBECTL_DRY_RUN} -k overlays/svc-cluster

undeploy-config:
	kubectl delete -k overlays/svc-cluster

.PHONY: deploy deploy-config undeploy-config
