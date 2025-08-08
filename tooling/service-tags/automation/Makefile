# Service Tags Tooling - Azure Monitor Workspace Deployment
# ============================================================

# Configuration variables
RESOURCE_GROUP_NAME ?= test-service-tags-tooling
LOCATION ?= eastus
ENVIRONMENT ?= test
NAME_PREFIX ?= service-tags
DEPLOYMENT_NAME ?= service-tags-workspace-$(shell date +%Y%m%d-%H%M%S)

# Azure CLI configuration
SUBSCRIPTION_ID ?= $(shell az account show --query id --output tsv 2>/dev/null)
MANAGED_IDENTITY_PRINCIPAL_ID ?= $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.managedIdentityPrincipalId.value" --output tsv 2>/dev/null)

# Colors for output
GREEN=\033[0;32m
YELLOW=\033[1;33m
RED=\033[0;31m
NC=\033[0m # No Color

.PHONY: help
help: ## Show this help message
	@echo "Service Tags Tooling - Azure Monitor Workspace Deployment"
	@echo "=========================================================="
	@echo ""
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  $(YELLOW)%-20s$(NC) %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Configuration:"
	@echo "  RESOURCE_GROUP_NAME: $(RESOURCE_GROUP_NAME)"
	@echo "  LOCATION:           $(LOCATION)"
	@echo "  ENVIRONMENT:        $(ENVIRONMENT)"
	@echo "  NAME_PREFIX:        $(NAME_PREFIX)"
	@echo "  SUBSCRIPTION_ID:    $(SUBSCRIPTION_ID)"

.PHONY: check-prereqs
check-prereqs: ## Check prerequisites (Azure CLI, authentication)
	@echo "$(YELLOW)Checking prerequisites...$(NC)"
	@which az > /dev/null || (echo "$(RED)Azure CLI not found. Please install it first.$(NC)" && exit 1)
	@az account show > /dev/null 2>&1 || (echo "$(RED)Not authenticated to Azure. Run 'az login' first.$(NC)" && exit 1)
	@echo "$(GREEN)✓ Azure CLI found and authenticated$(NC)"
	@echo "$(GREEN)✓ Current subscription: $(SUBSCRIPTION_ID)$(NC)"

.PHONY: create-resource-group
create-resource-group: check-prereqs ## Create the resource group if it doesn't exist
	@echo "$(YELLOW)Creating resource group if it doesn't exist...$(NC)"
	@az group create \
		--name $(RESOURCE_GROUP_NAME) \
		--location $(LOCATION) \
		--output table
	@echo "$(GREEN)✓ Resource group ready$(NC)"

.PHONY: validate
validate: check-prereqs ## Validate the Bicep template
	@echo "$(YELLOW)Validating Bicep template...$(NC)"
	@az deployment group validate \
		--resource-group $(RESOURCE_GROUP_NAME) \
		--template-file deploy-workspace.bicep \
		--parameters location=$(LOCATION) environment=$(ENVIRONMENT) namePrefix=$(NAME_PREFIX) \
		--output table
	@echo "$(GREEN)✓ Template validation successful$(NC)"

.PHONY: what-if
what-if: check-prereqs create-resource-group ## Show what resources will be created/modified
	@echo "$(YELLOW)Running what-if analysis...$(NC)"
	@az deployment group what-if \
		--resource-group $(RESOURCE_GROUP_NAME) \
		--template-file deploy-workspace.bicep \
		--parameters location=$(LOCATION) environment=$(ENVIRONMENT) namePrefix=$(NAME_PREFIX)

.PHONY: deploy
deploy: check-prereqs create-resource-group validate ## Deploy the Azure Monitor workspace
	@echo "$(YELLOW)Deploying Azure Monitor workspace...$(NC)"
	@az deployment group create \
		--resource-group $(RESOURCE_GROUP_NAME) \
		--name $(DEPLOYMENT_NAME) \
		--template-file deploy-workspace.bicep \
		--parameters location=$(LOCATION) environment=$(ENVIRONMENT) namePrefix=$(NAME_PREFIX) \
		--output table
	@echo "$(GREEN)✓ Deployment completed$(NC)"
	@$(MAKE) upload-runbook
	@$(MAKE) show-outputs

.PHONY: deploy-full
deploy-full: deploy deploy-current-subscription-permissions ## Deploy workspace and current subscription permissions
	@echo "$(GREEN)✓ Full deployment completed - workspace and current subscription permissions$(NC)"

.PHONY: upload-runbook
upload-runbook: ## Upload the PowerShell runbook content and create job schedule
	@echo "$(YELLOW)Uploading runbook content...$(NC)"
	@$(eval AUTOMATION_ACCOUNT := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.automationAccountName.value" --output tsv 2>/dev/null))
	@$(eval WORKSPACE_ENDPOINT := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.dataCollectionEndpointUrl.value" --output tsv 2>/dev/null))
	@$(eval RULE_ID := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.dataCollectionRuleId.value" --output tsv 2>/dev/null))
	@$(eval STREAM_NAME := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.streamName.value" --output tsv 2>/dev/null))
	@if [ -n "$(AUTOMATION_ACCOUNT)" ]; then \
		echo "Uploading to automation account: $(AUTOMATION_ACCOUNT)"; \
		az automation runbook replace-content \
			--automation-account-name $(AUTOMATION_ACCOUNT) \
			--resource-group $(RESOURCE_GROUP_NAME) \
			--runbook-name ServiceTagMonitoringPython \
			--content @automation-runbook-fixed.py; \
		echo "Publishing runbook..."; \
		az automation runbook publish \
			--automation-account-name $(AUTOMATION_ACCOUNT) \
			--resource-group $(RESOURCE_GROUP_NAME) \
			--runbook-name ServiceTagMonitoringPython; \
		echo "Note: Job schedule linking must be done manually in Azure portal or via REST API"; \
		echo "  - Go to Automation Account > Runbooks > ServiceTagMonitoring > Link to schedule"; \
		echo "  - Select schedule: ServiceTagMonitoring-Schedule"; \
		echo "  - Configure parameters: WorkspaceEndpoint=$(WORKSPACE_ENDPOINT), RuleId=$(RULE_ID), StreamName=$(STREAM_NAME)"; \
		echo "$(GREEN)✓ Runbook uploaded, published, and scheduled$(NC)"; \
	else \
		echo "$(RED)Error: Automation account not found$(NC)"; \
		exit 1; \
	fi

.PHONY: test-python-params
test-python-params: ## Test Python parameter passing in automation
	@echo "$(YELLOW)Testing Python parameter handling...$(NC)"
	@$(eval AUTOMATION_ACCOUNT := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.automationAccountName.value" --output tsv 2>/dev/null))
	@$(eval WORKSPACE_ENDPOINT := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.dataCollectionEndpointUrl.value" --output tsv 2>/dev/null))
	@$(eval RULE_ID := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.dataCollectionRuleId.value" --output tsv 2>/dev/null))
	@$(eval STREAM_NAME := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.streamName.value" --output tsv 2>/dev/null))
	@$(eval MANAGED_IDENTITY_CLIENT_ID := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.managedIdentityClientId.value" --output tsv 2>/dev/null))
	@if [ -n "$(AUTOMATION_ACCOUNT)" ]; then \
		echo "Creating test runbook..."; \
		az automation runbook create \
			--automation-account-name $(AUTOMATION_ACCOUNT) \
			--resource-group $(RESOURCE_GROUP_NAME) \
			--name TestPythonParams \
			--type Python3 \
			--description "Test Python parameter handling" || true; \
		echo "Uploading test script..."; \
		az automation runbook replace-content \
			--automation-account-name $(AUTOMATION_ACCOUNT) \
			--resource-group $(RESOURCE_GROUP_NAME) \
			--runbook-name TestPythonParams \
			--content @simple-param-test.py; \
		echo "Publishing test runbook..."; \
		az automation runbook publish \
			--automation-account-name $(AUTOMATION_ACCOUNT) \
			--resource-group $(RESOURCE_GROUP_NAME) \
			--runbook-name TestPythonParams; \
		echo "Starting test runbook..."; \
		az automation runbook start \
			--automation-account-name $(AUTOMATION_ACCOUNT) \
			--resource-group $(RESOURCE_GROUP_NAME) \
			--runbook-name TestPythonParams \
			--parameters WorkspaceEndpoint="$(WORKSPACE_ENDPOINT)" RuleId="$(RULE_ID)" StreamName="$(STREAM_NAME)" ManagedIdentityClientId="$(MANAGED_IDENTITY_CLIENT_ID)"; \
		echo "$(GREEN)✓ Test runbook started$(NC)"; \
	else \
		echo "$(RED)Error: Automation account not found$(NC)"; \
		exit 1; \
	fi

.PHONY: start-runbook
start-runbook: ## Start the automation runbook manually for testing
	@echo "$(YELLOW)Starting automation runbook...$(NC)"
	@$(eval AUTOMATION_ACCOUNT := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.automationAccountName.value" --output tsv 2>/dev/null))
	@$(eval WORKSPACE_ENDPOINT := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.dataCollectionEndpointUrl.value" --output tsv 2>/dev/null))
	@$(eval RULE_ID := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.dataCollectionRuleId.value" --output tsv 2>/dev/null))
	@$(eval STREAM_NAME := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.streamName.value" --output tsv 2>/dev/null))
	@$(eval MANAGED_IDENTITY_CLIENT_ID := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.managedIdentityClientId.value" --output tsv 2>/dev/null))
	@if [ -n "$(AUTOMATION_ACCOUNT)" ]; then \
		echo "Starting runbook in automation account: $(AUTOMATION_ACCOUNT)"; \
		az automation runbook start \
			--automation-account-name $(AUTOMATION_ACCOUNT) \
			--resource-group $(RESOURCE_GROUP_NAME) \
			--runbook-name ServiceTagMonitoringPython \
			--parameters WorkspaceEndpoint="$(WORKSPACE_ENDPOINT)" RuleId="$(RULE_ID)" StreamName="$(STREAM_NAME)" ManagedIdentityClientId="$(MANAGED_IDENTITY_CLIENT_ID)"; \
		echo "$(GREEN)✓ Runbook started successfully$(NC)"; \
	else \
		echo "$(RED)Error: Automation account not found$(NC)"; \
		exit 1; \
	fi

.PHONY: show-outputs
show-outputs: ## Show deployment outputs with usage instructions
	@echo "$(YELLOW)Deployment Outputs:$(NC)"
	@echo "==================="
	@az deployment group show \
		--resource-group $(RESOURCE_GROUP_NAME) \
		--name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) \
		--query "properties.outputs" \
		--output table
	@echo ""
	@echo "$(GREEN)Usage Examples:$(NC)"
	@echo "==============="
	@echo "PowerShell Automation Runbook:"
	@az deployment group show \
		--resource-group $(RESOURCE_GROUP_NAME) \
		--name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) \
		--query "properties.outputs.usageInstructions.value.powershellExample" \
		--output tsv
	@echo ""
	@echo "Query Example:"
	@az deployment group show \
		--resource-group $(RESOURCE_GROUP_NAME) \
		--name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) \
		--query "properties.outputs.usageInstructions.value.queryExample" \
		--output tsv

.PHONY: test-scripts
test-scripts: ## Test the service tag scripts with the deployed workspace
	@echo "$(YELLOW)Testing service tag scripts...$(NC)"
	@echo "Getting deployment outputs..."
	@$(eval WORKSPACE_ENDPOINT := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.dataCollectionEndpointUrl.value" --output tsv))
	@$(eval RULE_ID := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.dataCollectionRuleId.value" --output tsv))
	@$(eval STREAM_NAME := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.streamName.value" --output tsv))
	@echo "Testing PowerShell automation runbook..."
	@pwsh automation-runbook.ps1 -WorkspaceEndpoint "$(WORKSPACE_ENDPOINT)" -RuleId "$(RULE_ID)" -StreamName "$(STREAM_NAME)" -SubscriptionId "$(SUBSCRIPTION_ID)"
	@echo "$(GREEN)✓ PowerShell automation runbook test completed$(NC)"

.PHONY: clean
clean: check-prereqs ## Delete the resource group and all resources
	@echo "$(RED)WARNING: This will delete the entire resource group and all resources!$(NC)"
	@read -p "Are you sure? [y/N] " -n 1 -r; \
	echo ""; \
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then \
		echo "$(YELLOW)Deleting resource group...$(NC)"; \
		az group delete --name $(RESOURCE_GROUP_NAME) --yes --no-wait; \
		echo "$(GREEN)✓ Resource group deletion initiated$(NC)"; \
	else \
		echo "$(YELLOW)Deletion cancelled$(NC)"; \
	fi

.PHONY: status
status: check-prereqs ## Show the status of the deployment
	@echo "$(YELLOW)Deployment Status:$(NC)"
	@echo "=================="
	@az group exists --name $(RESOURCE_GROUP_NAME) > /dev/null && echo "$(GREEN)✓ Resource group exists$(NC)" || echo "$(RED)✗ Resource group does not exist$(NC)"
	@az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')]" --output table 2>/dev/null || echo "No deployments found"

.PHONY: deploy-subscription-permissions
deploy-subscription-permissions: check-prereqs ## Deploy Reader permissions to current subscription
	@echo "$(YELLOW)Deploying subscription-level Reader permissions...$(NC)"
	@if [ -z "$(MANAGED_IDENTITY_PRINCIPAL_ID)" ]; then \
		echo "$(RED)Error: Managed identity principal ID not found. Deploy the workspace first.$(NC)"; \
		exit 1; \
	fi
	@az deployment sub create \
		--location $(LOCATION) \
		--template-file deploy-subscription-permissions.bicep \
		--parameters managedIdentityPrincipalId=$(MANAGED_IDENTITY_PRINCIPAL_ID) environment=$(ENVIRONMENT) namePrefix=$(NAME_PREFIX) \
		--output table
	@echo "$(GREEN)✓ Subscription permissions deployed$(NC)"

.PHONY: deploy-current-subscription-permissions
deploy-current-subscription-permissions: check-prereqs ## Deploy Reader permissions to current subscription only
	@echo "$(YELLOW)Deploying Reader permissions to current subscription: $(SUBSCRIPTION_ID)...$(NC)"
	@if [ -z "$(MANAGED_IDENTITY_PRINCIPAL_ID)" ]; then \
		echo "$(RED)Error: Managed identity principal ID not found. Deploy the workspace first.$(NC)"; \
		exit 1; \
	fi
	@az deployment sub create \
		--subscription $(SUBSCRIPTION_ID) \
		--location $(LOCATION) \
		--template-file deploy-subscription-permissions.bicep \
		--parameters managedIdentityPrincipalId=$(MANAGED_IDENTITY_PRINCIPAL_ID) environment=$(ENVIRONMENT) namePrefix=$(NAME_PREFIX) \
		--output table
	@echo "$(GREEN)✓ Current subscription permissions deployed$(NC)"

.PHONY: logs
logs: ## Show recent logs from the workspace
	@echo "$(YELLOW)Recent logs from workspace:$(NC)"
	@$(eval WORKSPACE_NAME := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.workspaceName.value" --output tsv 2>/dev/null))
	@$(eval TABLE_NAME := $(shell az deployment group show --resource-group $(RESOURCE_GROUP_NAME) --name $(shell az deployment group list --resource-group $(RESOURCE_GROUP_NAME) --query "[?contains(name, 'service-tags-workspace')].name | [0]" --output tsv) --query "properties.outputs.customTableName.value" --output tsv 2>/dev/null))
	@if [ -n "$(WORKSPACE_NAME)" ] && [ -n "$(TABLE_NAME)" ]; then \
		az monitor log-analytics query \
			--workspace $(WORKSPACE_NAME) \
			--analytics-query "$(TABLE_NAME) | where TimeGenerated > ago(1h) | order by TimeGenerated desc | limit 10" \
			--output table; \
	else \
		echo "$(RED)Workspace not found or not deployed$(NC)"; \
	fi

# Default target
.DEFAULT_GOAL := help