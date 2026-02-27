using '../templates/output-region.bicep'

param azureMonitorWorkspaceName = '{{ .monitoring.svcWorkspaceName }}'
param hcpAzureMonitorWorkspaceName = '{{ .monitoring.hcpWorkspaceName }}'
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param auditLogsEventHubNamespaceName = '{{ .auditLogsEventHub.namespace }}'
param auditLogsEventHubName = '{{ .auditLogsEventHub.name }}'
param auditLogsEventHubAuthRuleName = '{{ .auditLogsEventHub.authRuleName }}'
param kustoEnabled = {{ .arobit.kusto.enabled }}
