@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitorWorkspaceName string

@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param hcpAzureMonitorWorkspaceName string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

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

resource maestroEventGridNamespace 'Microsoft.EventGrid/namespaces@2025-07-15-preview' existing = {
  name: maestroEventGridNamespacesName
}

output maestroEventGridNamespaceId string = maestroEventGridNamespace.id
output maestroEventGridNamespacesHostname string = maestroEventGridNamespace.properties.topicSpacesConfiguration.hostname
