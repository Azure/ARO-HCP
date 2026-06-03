@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitorWorkspaceName string

@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param hcpAzureMonitorWorkspaceName string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The name of the Cosmos DB for the RP')
param rpCosmosDbName string

//
//   A Z U R E   M O N I T O R
//

resource monitor 'microsoft.monitor/accounts@2021-06-03-preview' existing = {
  name: azureMonitorWorkspaceName
}

resource hcpMonitor 'microsoft.monitor/accounts@2021-06-03-preview' existing = {
  name: hcpAzureMonitorWorkspaceName
}

output azureMonitoringWorkspaceId string = monitor.id
output monitorPrometheusQueryEndpoint string = monitor.properties.metrics.prometheusQueryEndpoint

output hcpAzureMonitoringWorkspaceId string = hcpMonitor.id
output hcpMonitorPrometheusQueryEndpoint string = hcpMonitor.properties.metrics.prometheusQueryEndpoint

//
//  E V E N T G R I D
//

resource maestroEventGridNamespace 'Microsoft.EventGrid/namespaces@2024-06-01-preview' existing = {
  name: maestroEventGridNamespacesName
}

output maestroEventGridNamespaceId string = maestroEventGridNamespace.id
output maestroEventGridNamespacesHostname string = maestroEventGridNamespace.properties.topicSpacesConfiguration.hostname

//
//  C O S M O S D B
//

resource rpCosmosDbAccount 'Microsoft.DocumentDB/databaseAccounts@2023-11-15' existing = {
  name: rpCosmosDbName
}

output rpCosmosDbAccountId string = rpCosmosDbAccount.id
output cosmosDBDocumentEndpoint string = rpCosmosDbAccount.properties.documentEndpoint

//
//  A Z U R E   O P E N A I   ( H O L M E S )
//

@description('Whether Holmes Azure OpenAI is enabled')
param holmesEnabled bool = false

@description('The name of the Azure OpenAI account for Holmes')
param holmesAoaiName string = ''

resource holmesAoaiAccount 'Microsoft.CognitiveServices/accounts@2024-10-01' existing = if (holmesEnabled && holmesAoaiName != '') {
  name: holmesAoaiName
}

output holmesAoaiEndpoint string = holmesEnabled && holmesAoaiName != '' ? holmesAoaiAccount!.properties.endpoint : ''
output holmesAoaiResourceId string = holmesEnabled && holmesAoaiName != '' ? holmesAoaiAccount!.id : ''
