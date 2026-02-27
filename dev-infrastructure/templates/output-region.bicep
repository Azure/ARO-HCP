@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param azureMonitorWorkspaceName string

@description('The name of the Azure Monitor Workspace (stores prometheus metrics)')
param hcpAzureMonitorWorkspaceName string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('Toggle if instance is expected to exist')
param kustoEnabled bool

@description('Event Hub name for AKS audit logs')
param auditLogsEventHubName string

@description('Event Hub namespace for AKS audit logs')
param auditLogsEventHubNamespaceName string

@description('Name of the event hub authorization rule for AKS audit logs')
param auditLogsEventHubAuthRuleName string

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
// AUDIT LOGS EVENT HUB
//
resource auditLogsEventHubNamespace 'Microsoft.EventHub/namespaces@2024-01-01' existing = if (kustoEnabled) {
  name: auditLogsEventHubNamespaceName

  resource diagnosticSettingsAuthRule 'authorizationRules@2024-01-01' existing = {
    name: auditLogsEventHubAuthRuleName
  }
}

output auditLogsEventHubAuthRuleId string = kustoEnabled
  ? auditLogsEventHubNamespace::diagnosticSettingsAuthRule.id
  : ''
