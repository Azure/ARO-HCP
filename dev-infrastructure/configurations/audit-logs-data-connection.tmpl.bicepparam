using '../templates/audit-logs-data-connection.bicep'

param location = '{{ .kusto.location }}'
param kustoName = '{{ .kusto.kustoName }}'
param auditLogsKustoConsumerGroupName = '{{ .auditLogsEventHub.kustoConsumerGroupName }}'
param auditLogsEventHubId = '__auditLogsEventHubId__'
param databaseName = '{{ .kusto.serviceLogsDatabase }}'
param kustoDataConnectionName = '{{ .auditLogsEventHub.kustoDataConnectionName }}'
param kustoEnabled = {{ .arobit.kusto.enabled }}
param eventhubEnabled = {{ .auditLogsEventHub.enabled }}

