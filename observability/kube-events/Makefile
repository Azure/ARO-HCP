-include ../setup-env.mk

deploy:
	kubectl create namespace monitoring --dry-run=client -o json | kubectl apply -f - && \
	../../hack/helm.sh kube-events deploy monitoring \
		--set image.registry=${IMAGE_REGISTRY} \
		--set image.repository=${IMAGE_REPOSITORY} \
		--set image.digest=${IMAGE_DIGEST} \
		--set enabled=${ENABLED}
.PHONY: deploy

undeploy:
	helm uninstall kube-events --namespace monitoring
.PHONY: undeploy
