import { monitoringWorkspaceRefFromId } from '../modules/resource.bicep'

@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitorWorkspaceName string

@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param hcpAzureMonitorWorkspaceName string

param svcWorkspaceResourceId string = ''
param hcpWorkspaceResourceId string = ''

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The name of the Cosmos DB for the RP')
param rpCosmosDbName string

//
//   A Z U R E   M O N I T O R
//

var svcWorkspace = monitoringWorkspaceRefFromId(svcWorkspaceResourceId != ''
  ? svcWorkspaceResourceId
  : resourceId('Microsoft.Monitor/accounts', azureMonitorWorkspaceName))
var hcpWorkspace = monitoringWorkspaceRefFromId(hcpWorkspaceResourceId != ''
  ? hcpWorkspaceResourceId
  : resourceId('Microsoft.Monitor/accounts', hcpAzureMonitorWorkspaceName))

resource monitor 'microsoft.monitor/accounts@2021-06-03-preview' existing = {
  scope: resourceGroup(svcWorkspace.resourceGroup.subscriptionId, svcWorkspace.resourceGroup.name)
  name: svcWorkspace.name
}

resource hcpMonitor 'microsoft.monitor/accounts@2021-06-03-preview' existing = {
  scope: resourceGroup(hcpWorkspace.resourceGroup.subscriptionId, hcpWorkspace.resourceGroup.name)
  name: hcpWorkspace.name
}

output azureMonitoringWorkspaceId string = monitor.id
output azureMonitorWorkspaceLocation string = monitor.location
output monitorPrometheusQueryEndpoint string = monitor.properties.metrics.prometheusQueryEndpoint

output hcpAzureMonitoringWorkspaceId string = hcpMonitor.id
output hcpAzureMonitorWorkspaceLocation string = hcpMonitor.location
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
