deploy: deploy-service

deploy-service:
	kubectl apply -k overlays/svc

undeploy-service:
	kubectl delete -k overlays/svc

.PHONY: deploy deploy-service undeploy-service
