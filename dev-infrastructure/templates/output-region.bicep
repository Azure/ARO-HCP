@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitorWorkspaceName string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('Enable Log Analytics')
param enableLogAnalytics bool

//
//   A Z U R E   M O N I T O R
//

resource monitor 'microsoft.monitor/accounts@2021-06-03-preview' existing = {
  name: azureMonitorWorkspaceName
}

output azureMonitoringWorkspaceId string = monitor.id
output monitorPrometheusQueryEndpoint string = monitor.properties.metrics.prometheusQueryEndpoint

//
//  E V E N T G R I D
//

resource maestroEventGridNamespace 'Microsoft.EventGrid/namespaces@2024-06-01-preview' existing = {
  name: maestroEventGridNamespacesName
}

output maestroEventGridNamespaceId string = maestroEventGridNamespace.id
output maestroEventGridNamespacesHostname string = maestroEventGridNamespace.properties.topicSpacesConfiguration.hostname

//
//   L O G   A N A L Y T I C S
//

resource logAnalyticsWorkspace 'Microsoft.OperationalInsights/workspaces@2023-09-01' existing = if (enableLogAnalytics) {
  name: 'log-analytics-workspace'
}

output logAnalyticsWorkspaceId string = enableLogAnalytics ? logAnalyticsWorkspace.id : ''
