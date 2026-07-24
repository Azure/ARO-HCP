include ./../../Makefile

CLUSTER_NAME     = $(shell yq .kusto.name $(CONFIG_FILE))
REGION           = $(shell yq .region $(CONFIG_FILE))
HCP_DATABASE     = $(shell yq .kusto.hostedControlPlaneLogsDatabase $(CONFIG_FILE))
SERVICE_DATABASE = $(shell yq .kusto.serviceLogsDatabase $(CONFIG_FILE))

run: $(TRIAGE)
	$(TRIAGE) kusto-query --cluster-name $(CLUSTER_NAME) --region $(REGION) --hcp-database $(HCP_DATABASE) --service-database $(SERVICE_DATABASE) --start-time $(START_TIME) --end-time $(END_TIME) --resource-group $(RESOURCE_GROUP) --kql-file $(KQL_FILE) $(foreach k,$(EXTRA_VARS),--extra-var $(k))
.PHONY: run
