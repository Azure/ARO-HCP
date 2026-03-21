using '../templates/output-audit-logs-eventhub.bicep'

param auditLogsEventHubNamespaceName = '{{ .auditLogsEventHub.namespace }}'
param auditLogsEventHubAuthRuleName = '{{ .auditLogsEventHub.authRuleName }}'
param kustoEnabled = {{ .arobit.kusto.enabled }}
