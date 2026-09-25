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
param bootstrapTables bool

resource kustoCluster 'Microsoft.Kusto/clusters@2024-04-13' existing = if (kustoEnabled) {
  name: kustoName
}

// Dev does not run the Geography Kusto rollout, so the Region data connection needs its tables here.
module maestroMqttTableScript '../modules/logs/kusto/script.bicep' = if (kustoEnabled && eventhubEnabled && bootstrapTables) {
  name: 'maestroMqttConnectionsBootstrap'
  params: {
    kustoName: kustoName
    databaseName: databaseName
    scriptName: 'maestroMqttConnections-bootstrap'
    principalPermissionsAction: 'RemovePermissionOnScriptCompletion'
    scriptContent: loadTextContent('../modules/logs/kusto/tables/maestroMqttConnections.kql')
    continueOnErrors: false
  }
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
  dependsOn: [maestroMqttTableScript]
}
