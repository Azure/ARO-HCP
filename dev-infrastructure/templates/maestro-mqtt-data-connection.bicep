@description('Name of the existing Kusto cluster')
param kustoName string

@description('ServiceLogs database name')
param databaseName string

@description('Resource ID of the Maestro MQTT diagnostics Event Hub')
param maestroMqttEventHubId string

@description('Consumer group for the Kusto data connection')
param kustoConsumerGroupName string

@description('Name of the Kusto data connection')
param kustoDataConnectionName string

@description('Azure region')
param location string = resourceGroup().location

param kustoEnabled bool
param eventhubEnabled bool

resource kustoCluster 'Microsoft.Kusto/clusters@2024-04-13' existing = if (kustoEnabled) {
  name: kustoName
}

resource maestroMqttDataConnection 'Microsoft.Kusto/clusters/databases/dataConnections@2024-04-13' = if (kustoEnabled && eventhubEnabled) {
  name: '${kustoName}/${databaseName}/${kustoDataConnectionName}'
  location: location
  kind: 'EventHub'
  properties: {
    eventHubResourceId: maestroMqttEventHubId
    consumerGroup: kustoConsumerGroupName
    tableName: 'rawMaestroMqttConnections'
    dataFormat: 'JSON'
    compression: 'None'
    mappingRuleName: 'rawMaestroMqttConnectionsMapping'
    managedIdentityResourceId: kustoCluster.id
  }
}
