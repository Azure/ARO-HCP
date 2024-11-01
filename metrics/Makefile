deploy: deploy-config

deploy-config:
	kubectl apply -k overlays/svc-cluster

undeploy-config:
	kubectl delete -k overlays/svc-cluster

.PHONY: deploy deploy-config undeploy-config
