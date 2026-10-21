using '../templates/alert-events-data-connection.bicep'

param location = '{{ .kusto.location }}'
param kustoName = '{{ .kusto.kustoName }}'
param alertEventsKustoConsumerGroupName = '{{ .alertEventsEventHub.kustoConsumerGroupName }}'
param alertEventsEventHubId = '__alertEventsEventHubId__'
param databaseName = '{{ .kusto.monitoringEventsDatabase }}'
param kustoDataConnectionName = '{{ .alertEventsEventHub.kustoDataConnectionName }}'
param kustoEnabled = {{ .arobit.kusto.enabled }}
param eventhubEnabled = {{ .auditLogsEventHub.enabled }}
