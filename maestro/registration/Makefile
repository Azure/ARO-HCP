SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk

deploy:
	@if ! kubectl get service maestro -n maestro > /dev/null 2>&1; then \
		echo "Error: Service 'maestro' not found in namespace 'maestro'"; \
		exit 1; \
	fi
	helm upgrade --install ${CONSUMER_NAME} ./helm \
		--namespace maestro \
		--set consumerName=${CONSUMER_NAME}
.PHONY: deploy
