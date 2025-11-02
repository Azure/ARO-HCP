all: deploy
	@echo "Observability services are now configured and enabled."
	@echo "Run the following command to port-forward traffic to the Jaeger or LGTM service:"
	@echo ""
	@echo "Jaeger"
	@echo "kubectl port-forward -n observability svc/jaeger 16686:16686"
	@echo "LGTM"
	@echo "kubectl port-forward -n observability svc/lgtm 3000:3000"
	@echo ""
	@echo "Then open 'http://localhost:16686' or 'http://localhost:3000' in your browser."

deploy:
	@if [ "$(DRY_RUN)" != "true" ]; then \
		kubectl apply -k deploy/; \
		kubectl wait --for=condition=Available deployment -n observability otel-collector --timeout=60s; \
		kubectl wait --for=condition=Available deployment -n observability jaeger --timeout=60s; \
		kubectl wait --for=condition=Available deployment -n observability lgtm --timeout=60s; \
	else \
		echo "Skipping deployment because DRY_RUN is set to true"; \
	fi
.PHONY: deploy
