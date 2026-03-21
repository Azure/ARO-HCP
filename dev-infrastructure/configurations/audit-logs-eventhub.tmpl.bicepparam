using '../templates/audit-logs-eventhub.bicep'

param auditLogsKustoConsumerGroupName = '{{ .auditLogsEventHub.kustoConsumerGroupName }}'
param auditLogsDiagnosticSettingsRuleName = '{{ .auditLogsEventHub.authRuleName }}'
param auditLogsEventHubName = '{{ .auditLogsEventHub.name }}'
param auditLogsEventHubNamespaceName = '{{ .auditLogsEventHub.namespace }}'
param manageInstance = {{ .kusto.manageInstance }}
param kustoPrincipalId = '__kustoPrincipalId__'
