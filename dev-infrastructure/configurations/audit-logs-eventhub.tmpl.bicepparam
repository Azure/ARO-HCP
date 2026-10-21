using '../templates/audit-logs-eventhub.bicep'

param auditLogsKustoConsumerGroupName = '{{ .auditLogsEventHub.kustoConsumerGroupName }}'
param auditLogsDiagnosticSettingsRuleName = '{{ .auditLogsEventHub.authRuleName }}'
param auditLogsEventHubName = '{{ .auditLogsEventHub.name }}'
param auditLogsEventHubNamespaceName = '{{ .auditLogsEventHub.namespace }}'
param alertEventsEventHubName = '{{ .alertEventsEventHub.name }}'
param alertEventsKustoConsumerGroupName = '{{ .alertEventsEventHub.kustoConsumerGroupName }}'
param maestroMqttEventHubName = '{{ .maestroMqttEventHub.name }}'
param maestroMqttKustoConsumerGroupName = '{{ .maestroMqttEventHub.kustoConsumerGroupName }}'
param maestroMqttEnabled = {{ .maestroMqttEventHub.enabled }}
param eventhubEnabled = {{ .auditLogsEventHub.enabled }}
// EV2 substitutes the SKU after compilation; any() accepts its placeholder while preserving the template's allowed values.
param eventHubSku = any('{{ .auditLogsEventHub.sku }}')
param kustoPrincipalId = '__kustoPrincipalId__'
param kustoEnabled = {{ .arobit.kusto.enabled }}
