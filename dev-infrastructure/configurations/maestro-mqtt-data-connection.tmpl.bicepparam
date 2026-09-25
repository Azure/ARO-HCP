using '../templates/maestro-mqtt-data-connection.bicep'

param location = '{{ .kusto.location }}'
param kustoName = '{{ .kusto.kustoName }}'
param databaseName = '{{ .kusto.serviceLogsDatabase }}'
param maestroMqttEventHubId = '__maestroMqttEventHubId__'
param kustoConsumerGroupName = '{{ .maestroMqttEventHub.kustoConsumerGroupName }}'
param kustoDataConnectionName = '{{ .maestroMqttEventHub.kustoDataConnectionName }}'
param kustoEnabled = {{ .arobit.kusto.enabled }}
param eventhubEnabled = {{ .auditLogsEventHub.enabled }}
