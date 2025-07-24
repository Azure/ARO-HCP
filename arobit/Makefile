-include ../setup-env.mk
-include ../helm-cmd.mk

deploy:
	kubectl create namespace mds --dry-run=client -o json | kubectl apply -f - && \
	# ${HELM_CMD} arobit \
	helm template arobit \
		deploy/ \
		--namespace mds \
		--set forwarder.fluentbit.image.registry=${AROBIT_FORWARDER_REGISTRY} \
		--set forwarder.fluentbit.image.repository=${AROBIT_FORWARDER_REPOSITORY} \
		--set forwarder.fluentbit.image.digest=${AROBIT_FORWARDER_DIGEST} \
		--set forwarder.mdsd.image.registry=${AROBIT_MDSD_REGISTRY} \
		--set forwarder.mdsd.image.repository=${AROBIT_MDSD_REPOSITORY} \
		--set forwarder.mdsd.image.digest=${AROBIT_MDSD_DIGEST} \
		--set forwarder.mdsd.enabled=${AROBIT_MDSD_ENABLED} \
		--set forwarder.mdsd.geneva.accountName=${AROBIT_MDSD_GENEVA_ACCOUNT_NAME} \
		--set forwarder.mdsd.geneva.san=${AROBIT_MDSD_GENEVA_SAN} \
		--set forwarder.mdsd.geneva.environment=${AROBIT_MDSD_GENEVA_ENVIRONMENT} \
		--set forwarder.mdsd.geneva.namespace=${AROBIT_MDSD_GENEVA_NAMESPACE} \
		--set forwarder.mdsd.geneva.region=${AROBIT_MDSD_GENEVA_REGION} \
		--set forwarder.clusterType=${AROBIT_CLUSTER_TYPE} \
		--set forwarder.secretProvider.enabled=${AROBIT_MDSD_ENABLED} \
		--set forwarder.secretProvider.keyVault=${AROBIT_SECRET_PROVIDER_KEY_VAULT} \
		--set forwarder.secretProvider.rpSecret=${AROBIT_SECRET_PROVIDER_RP_SECRET} > arobit.yaml
.PHONY: deploy

undeploy:
	helm uninstall arobit --namespace mds
.PHONY: undeploy

lint:
	helm lint deploy/
.PHONY: lint
