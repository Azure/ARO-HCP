@description('Name of the Kusto cluster to create')
param kustoName string

@description('Consumer group name for Kusto data connection')
param auditLogsKustoConsumerGroupName string

@description('ID of the Event Hub to connect to')
param auditLogsEventHubId string

@description('Target Kusto database name')
param databaseName string

@description('Kusto data connection resource name')
param kustoDataConnectionName string

// Kusto Event Hub data connection for AKS audit logs
resource kustoDataConnection 'Microsoft.Kusto/clusters/databases/dataConnections@2024-04-13' = {
  name: '${kustoName}/${databaseName}/${kustoDataConnectionName}'
  location: resourceGroup().location
  kind: 'EventHub'
  properties: {
    eventHubResourceId: auditLogsEventHubId
    consumerGroup: auditLogsKustoConsumerGroupName
    tableName: 'rawAksEvents'
    dataFormat: 'JSON'
    compression: 'None'
    mappingRuleName: 'rawAksEventsMapping'
  }
}
