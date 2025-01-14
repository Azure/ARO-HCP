SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk
-include ../helm-cmd.mk
HELM_CMD ?= helm upgrade --install

deploy:
	@if ! kubectl get service maestro -n ${NAMESPACE} > /dev/null 2>&1; then \
		echo "Error: Service 'maestro' not found in namespace '${NAMESPACE}'"; \
		exit 1; \
	fi
	${HELM_CMD} ${CONSUMER_NAME} ./helm \
		--namespace ${NAMESPACE} \
		--set consumerName=${CONSUMER_NAME}
.PHONY: deploy
