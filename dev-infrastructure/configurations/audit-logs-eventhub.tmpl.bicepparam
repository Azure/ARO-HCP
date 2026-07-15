using '../templates/audit-logs-eventhub.bicep'

param auditLogsKustoConsumerGroupName = '{{ .auditLogsEventHub.kustoConsumerGroupName }}'
param auditLogsDiagnosticSettingsRuleName = '{{ .auditLogsEventHub.authRuleName }}'
param auditLogsEventHubName = '{{ .auditLogsEventHub.name }}'
param auditLogsEventHubNamespaceName = '{{ .auditLogsEventHub.namespace }}'
param alertEventsEventHubName = '{{ .alertEventsEventHub.name }}'
param alertEventsKustoConsumerGroupName = '{{ .alertEventsEventHub.kustoConsumerGroupName }}'
param eventhubEnabled = {{ .auditLogsEventHub.enabled }}
param kustoPrincipalId = '__kustoPrincipalId__'
param kustoEnabled = {{ .arobit.kusto.enabled }}
