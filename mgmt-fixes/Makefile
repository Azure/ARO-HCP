PIPELINE = "../dev-infrastructure/mgmt-pipeline.yaml"
PIPELINE_STEP = "mgmt-fixes"
-include ../setup-env.mk
-include ../helm-cmd.mk

deploy:
	@if [ "$(APPLY_KUBELET_FIXES)" = "true" ]; then \
		${HELM_CMD} mgmt-fixes deploy/helm/mgmt-fixes/; \
	else \
		echo "Skipping kubelet patching"; \
	fi
.PHONY: deploy


undeploy:
	helm uninstall mgmt-fixes
.PHONY: undeploy
