-include ../../setup-env.mk

all: deploy patch-frontend patch-clusterservice
	@echo "Observability services are now configured and enabled."
	@echo "Run the following command to port-forward traffic to the Jaeger service:"
	@echo ""
	@echo "kubectl port-forward -n observability svc/jaeger 16686:16686"
	@echo ""
	@echo "Then open 'http://localhost:16686' in your browser."

deploy:
	@if [ "$(DEPLOY)" = "true" ]; then \
		kubectl apply -k deploy/; \
		kubectl wait --for=condition=Available deployment -n observability jaeger --timeout=60s; \
	else \
		echo "Skipping deployment because DEPLOY is not set to true"; \
	fi
.PHONY: deploy

patch-frontend:
	@kubectl set env -n aro-hcp deployment aro-hcp-frontend --containers aro-hcp-frontend OTEL_EXPORTER_OTLP_ENDPOINT=http://ingest.observability:4318 OTEL_TRACES_EXPORTER=otlp
	@kubectl wait --for=condition=Available -n aro-hcp deployment aro-hcp-frontend --timeout=30s
.PHONY: patch-frontend

patch-maestro-server:
	@kubectl set env -n maestro deployment maestro --containers service OTEL_EXPORTER_OTLP_ENDPOINT=http://ingest.observability:4318 OTEL_TRACES_EXPORTER=otlp
	@kubectl wait --for=condition=Available -n maestro deployment maestro --timeout=30s
.PHONY: patch-maestro-server

patch-clusterservice:
	kubectl patch -n ${CS_NAMESPACE} deployment clusters-service --type json \
		--patch="[{\"op\": \"add\", \"path\": \"/spec/template/spec/containers/0/command/-\", \"value\": \"--tracing-otlp-url=http://ingest.observability:4318\"}]"
	@kubectl wait --for=condition=Available -n ${CS_NAMESPACE} deployment clusters-service --timeout=30s
.PHONY: patch-clusterservice
