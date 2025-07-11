-include ../setup-env.mk
-include ../helm-cmd.mk

deploy:
	kubectl create namespace mds --dry-run=client -o json | kubectl apply -f - && \
	${HELM_CMD} arobit \
		deploy/ \
		--namespace mds \
		--set forwarder.fluentbit.image.registry=${AROBIT_FORWARDER_REGISTRY} \
		--set forwarder.fluentbit.image.repository=${AROBIT_FORWARDER_REPOSITORY} \
		--set forwarder.fluentbit.image.digest=${AROBIT_FORWARDER_DIGEST} \
		--set forwarder.mdsd.image.registry=${AROBIT_MDSD_REGISTRY} \
		--set forwarder.mdsd.image.repository=${AROBIT_MDSD_REPOSITORY} \
		--set forwarder.mdsd.image.digest=${AROBIT_MDSD_DIGEST} \
		--set forwarder.mdsd.enabled=${AROBIT_MDSD_ENABLED}
.PHONY: deploy

undeploy:
	helm uninstall arobit --namespace mds
.PHONY: undeploy
