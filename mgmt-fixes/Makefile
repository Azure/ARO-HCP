PIPELINE = "../dev-infrastructure/mgmt-pipeline.yaml"
PIPELINE_STEP = "mgmt-fixes"
-include ../setup-env.mk

deploy:
	@if [ "$(APPLY_KUBELET_FIXES)" = "true" ]; then \
		../hack/helm.sh mgmt-fixes deploy/kubelet-ds default; \
	else \
		echo "Skipping kubelet patching"; \
	fi

	@kubectl create namespace aro-hcp-mitigations --dry-run=client -o json | kubectl apply -f - && \
	../hack/helm.sh mitigations deploy/mitigations aro-hcp-mitigations

	@kubectl annotate storageclass default storageclass.kubernetes.io/is-default-class-
	../hack/helm.sh default-sc deploy/default-sc default
.PHONY: deploy


undeploy:
	helm uninstall mgmt-fixes
.PHONY: undeploy
