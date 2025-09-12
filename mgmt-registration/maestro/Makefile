deploy:
	@if ! kubectl get service maestro -n ${NAMESPACE} > /dev/null 2>&1; then \
		echo "Error: Service 'maestro' not found in namespace '${NAMESPACE}'"; \
		exit 1; \
	fi
	../../hack/helm.sh ${CONSUMER_NAME} deploy ${NAMESPACE} \
		--set consumerName=${CONSUMER_NAME}
.PHONY: deploy
