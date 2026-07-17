include ./../../Makefile

CLUSTER_NAME     = $(shell yq .kusto.name $(CONFIG_FILE))
REGION           = $(shell yq .region $(CONFIG_FILE))
HCP_DATABASE     = $(shell yq .kusto.hostedControlPlaneLogsDatabase $(CONFIG_FILE))
SERVICE_DATABASE = $(shell yq .kusto.serviceLogsDatabase $(CONFIG_FILE))

run: $(TRIAGE)
	$(TRIAGE) trace-request --cluster-name $(CLUSTER_NAME) --region $(REGION) --hcp-database $(HCP_DATABASE) --service-database $(SERVICE_DATABASE) --start-time $(START_TIME) --end-time $(END_TIME) --correlation-id $(CORRELATION_ID) --output-dir $(OUTPUT_DIR)
.PHONY: run
