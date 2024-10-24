SHELL = /bin/bash

deploy:
	@if ! kubectl get service maestro -n maestro > /dev/null 2>&1; then \
		echo "Error: Service 'maestro' not found in namespace 'maestro'"; \
		exit 1; \
	fi
	helm upgrade --install {{ .maestroConsumerName }} ./helm \
		--namespace maestro \
		--set consumerName={{ .maestroConsumerName }}
.PHONY: deploy
