@description('Event Hub namespace name for AKS audit logs')
param auditLogsEventHubNamespaceName string

@description('Event Hub name for AKS audit logs')
param auditLogsEventHubName string

@description('Consumer group name for Kusto data connection')
param auditLogsKustoConsumerGroupName string

@description('Diagnostic settings authorization rule name')
param auditLogsDiagnosticSettingsRuleName string

// Event Hub namespace for AKS audit logs
resource eventHubNamespace 'Microsoft.EventHub/namespaces@2024-01-01' = {
  name: auditLogsEventHubNamespaceName
  location: resourceGroup().location
  sku: {
    name: 'Standard'
    tier: 'Standard'
    capacity: 1
  }
  properties: {
    minimumTlsVersion: '1.2'
  }

  // Authorization rule for diagnostic settings
  resource diagnosticSettingsAuthRule 'authorizationRules@2024-01-01' = {
    name: auditLogsDiagnosticSettingsRuleName
    properties: {
      rights: [
        'Send'
      ]
    }
  }

  // Event Hub for AKS audit logs
  resource eventHub 'eventhubs@2024-01-01' = {
    name: auditLogsEventHubName
    properties: {
      messageRetentionInDays: 7
      partitionCount: 2
    }

    // Consumer group for Kusto data connection
    resource kustoConsumerGroup 'consumergroups@2024-01-01' = {
      name: auditLogsKustoConsumerGroupName
    }
  }
}
output eventHubId string = eventHubNamespace::eventHub.id
