@description('Name of the Maestro Event Grid namespace')
param eventGridNamespaceName string

@description('Authorization rule for sending diagnostics to the regional Event Hub namespace')
param eventHubAuthorizationRuleId string

@description('Event Hub for Maestro MQTT connection diagnostics')
param eventHubName string

param kustoEnabled bool
param eventhubEnabled bool
param maestroMqttEnabled bool

resource eventGridNamespace 'Microsoft.EventGrid/namespaces@2024-12-15-preview' existing = if (kustoEnabled && eventhubEnabled && maestroMqttEnabled) {
  name: eventGridNamespaceName
}

resource maestroMqttDiagnostics 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (kustoEnabled && eventhubEnabled && maestroMqttEnabled) {
  scope: eventGridNamespace
  name: 'maestro-mqtt-connections'
  properties: {
    eventHubAuthorizationRuleId: eventHubAuthorizationRuleId
    eventHubName: eventHubName
    logs: [
      {
        category: 'FailedMqttConnections'
        enabled: true
      }
      {
        category: 'SuccessfulMqttConnections'
        enabled: true
      }
      {
        category: 'MqttDisconnections'
        enabled: true
      }
    ]
  }
}
