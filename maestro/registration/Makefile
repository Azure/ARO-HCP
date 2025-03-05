-include ../../helm-cmd.mk

deploy:
	@if ! kubectl get service maestro -n ${NAMESPACE} > /dev/null 2>&1; then \
		echo "Error: Service 'maestro' not found in namespace '${NAMESPACE}'"; \
		exit 1; \
	fi
	${HELM_CMD} ${CONSUMER_NAME} ./deploy \
		--namespace ${NAMESPACE} \
		--set consumerName=${CONSUMER_NAME}
.PHONY: deploy
