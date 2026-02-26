@description('Name of the Kusto cluster to lookup')
param kustoName string

@description('Toggle if instance is expected to exist')
param kustoEnabled bool

@description('Event Hub name for AKS audit logs')
param auditLogsEventHubName string

@description('Event Hub namespace for AKS audit logs')
param auditLogsEventHubNamespaceName string

@description('Name of the event hub authorization rule for AKS audit logs')
param auditLogsEventHubAuthRuleName string

resource kusto 'Microsoft.Kusto/clusters@2024-04-13' existing = if (kustoEnabled) {
  name: kustoName
}

resource auditLogsEventHubNamespace 'Microsoft.EventHub/namespaces@2024-01-01' existing = if (kustoEnabled) {
  name: auditLogsEventHubNamespaceName

  resource eventHub 'eventhubs@2024-01-01' existing = {
    name: auditLogsEventHubName
  }

  resource diagnosticSettingsAuthRule 'authorizationRules@2024-01-01' existing = {
    name: auditLogsEventHubAuthRuleName
  }
}

output kustoResourceId string = kustoEnabled ? kusto.id : ''
output kustoUri string = kustoEnabled ? kusto.properties.uri : ''
output kustoDataIngestionUri string = kustoEnabled ? kusto.properties.dataIngestionUri : ''
output auditLogsEventHubAuthRuleId string = kustoEnabled
  ? auditLogsEventHubNamespace::diagnosticSettingsAuthRule.id
  : ''
