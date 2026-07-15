@description('Name of the existing Kusto cluster where the data connection will be created')
param kustoName string

@description('Consumer group name for Kusto data connection')
param auditLogsKustoConsumerGroupName string

@description('ID of the Event Hub to connect to')
param auditLogsEventHubId string

@description('Target Kusto database name')
param databaseName string

@description('Kusto data connection resource name')
param kustoDataConnectionName string

@description('Azure Region Location')
param location string = resourceGroup().location

@description('Whether the arobit Kusto cluster is enabled in this region')
param kustoEnabled bool

@description('Whether the audit logs Event Hub is enabled in this region')
param eventhubEnabled bool

resource kustoCluster 'Microsoft.Kusto/clusters@2024-04-13' existing = if (kustoEnabled) {
  name: kustoName
}

// Kusto Event Hub data connection for AKS audit logs
resource kustoDataConnection 'Microsoft.Kusto/clusters/databases/dataConnections@2024-04-13' = if (kustoEnabled && eventhubEnabled) {
  name: '${kustoName}/${databaseName}/${kustoDataConnectionName}'
  location: location
  kind: 'EventHub'
  properties: {
    eventHubResourceId: auditLogsEventHubId
    consumerGroup: auditLogsKustoConsumerGroupName
    tableName: 'rawAksEvents'
    dataFormat: 'JSON'
    compression: 'None'
    mappingRuleName: 'rawAksEventsMapping'
    managedIdentityResourceId: kustoCluster.id
  }
}
