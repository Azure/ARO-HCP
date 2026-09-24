using '../templates/maestro-mqtt-diagnostics.bicep'

param eventGridNamespaceName = '{{ .maestro.eventGrid.name }}'
param eventHubAuthorizationRuleId = '__auditLogsEventHubAuthRuleId__'
param eventHubName = '{{ .maestroMqttEventHub.name }}'
param kustoEnabled = {{ .arobit.kusto.enabled }}
param eventhubEnabled = {{ .auditLogsEventHub.enabled }}
