-include ../setup-env.mk

deploy: deploy-service

deploy-service:
	kubectl apply ${KUBECTL_DRY_RUN} -k overlays/svc

undeploy-service:
	kubectl delete -k overlays/svc

.PHONY: deploy deploy-service undeploy-service
