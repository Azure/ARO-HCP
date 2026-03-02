@description('Event Hub namespace name for AKS audit logs')
param auditLogsEventHubNamespaceName string

@description('Event Hub name for AKS audit logs')
param auditLogsEventHubName string

@description('Consumer group name for Kusto data connection')
param auditLogsKustoConsumerGroupName string

@description('Diagnostic settings authorization rule name')
param auditLogsDiagnosticSettingsRuleName string

@description('Principal ID of the Kusto cluster managed identity')
param kustoPrincipalId string

// Event Hub namespace for AKS audit logs
resource eventHubNamespace 'Microsoft.EventHub/namespaces@2024-01-01' = {
  name: auditLogsEventHubNamespaceName
  location: resourceGroup().location
  sku: {
    name: 'Premium'
    tier: 'Premium'
    capacity: 1
  }
  properties: {
    minimumTlsVersion: '1.2'
    publicNetworkAccess: 'Disabled'
  }

  resource networkRuleSet 'networkRuleSets@2024-01-01' = {
    name: 'default'
    properties: {
      defaultAction: 'Deny'
      publicNetworkAccess: 'Disabled'
      trustedServiceAccessEnabled: true
    }
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

  // Event Hub for svc AKS audit logs
  resource eventHub 'eventhubs@2024-01-01' = {
    name: auditLogsEventHubName
    properties: {
      messageRetentionInDays: 7
      partitionCount: 2
    }

    // Consumer group for svc Kusto data connection
    resource kustoConsumerGroup 'consumergroups@2024-01-01' = {
      name: auditLogsKustoConsumerGroupName
    }
  }
}

var eventHubDataReceiverRole = 'a638d3c7-ab3a-418d-83e6-5f17a39d4fde'
resource eventHubDataReceiverRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: eventHubNamespace::eventHub
  name: guid(eventHubNamespace::eventHub.id, kustoPrincipalId, eventHubDataReceiverRole)
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', eventHubDataReceiverRole)
    principalId: kustoPrincipalId
    principalType: 'ServicePrincipal'
  }
}

output auditLogsEventHubId string = eventHubNamespace::eventHub.id
output auditLogsEventHubAuthRuleId string = eventHubNamespace::diagnosticSettingsAuthRule.id
