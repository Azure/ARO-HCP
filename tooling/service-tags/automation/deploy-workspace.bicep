@description('Location for all resources')
param location string = resourceGroup().location

@description('Environment name (dev, test, prod)')
param environment string = 'test'

@description('Name prefix for resources')
param namePrefix string = 'service-tags'

@description('Schedule start time for automation runbook')
param scheduleStartTime string = dateTimeAdd(utcNow(), 'PT30M')

var workspaceName = '${namePrefix}-${environment}-workspace'
var logAnalyticsWorkspaceName = '${namePrefix}-${environment}-logs'
var dataCollectionEndpointName = '${namePrefix}-${environment}-dce'
var dataCollectionRuleName = '${namePrefix}-${environment}-dcr'
var customTableName = 'ServiceTagMetrics_CL'
var streamName = 'Custom-${customTableName}'

// Log Analytics Workspace (required for DCR)
resource logAnalyticsWorkspace 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
  name: logAnalyticsWorkspaceName
  location: location
  tags: {
    environment: environment
    purpose: 'service-tags-monitoring'
  }
  properties: {
    sku: {
      name: 'PerGB2018'
    }
    retentionInDays: 30
    features: {
      enableLogAccessUsingOnlyResourcePermissions: true
    }
  }
}

// Custom table for service tag metrics
resource customTable 'Microsoft.OperationalInsights/workspaces/tables@2022-10-01' = {
  parent: logAnalyticsWorkspace
  name: customTableName
  properties: {
    totalRetentionInDays: 30
    plan: 'Analytics'
    schema: {
      name: customTableName
      columns: [
        {
          name: 'TimeGenerated'
          type: 'datetime'
        }
        {
          name: 'Name'
          type: 'string'
        }
        {
          name: 'Value'
          type: 'real'
        }
        {
          name: 'subscription'
          type: 'string'
        }
        {
          name: 'region'
          type: 'string'
        }
        {
          name: 'ipTagType'
          type: 'string'
        }
        {
          name: 'tag'
          type: 'string'
        }
      ]
    }
  }
}

// Data Collection Endpoint
resource dataCollectionEndpoint 'Microsoft.Insights/dataCollectionEndpoints@2022-06-01' = {
  name: dataCollectionEndpointName
  location: location
  tags: {
    environment: environment
    purpose: 'service-tags-monitoring'
  }
  properties: {
    networkAcls: {
      publicNetworkAccess: 'Enabled'
    }
  }
}

// Data Collection Rule
resource dataCollectionRule 'Microsoft.Insights/dataCollectionRules@2022-06-01' = {
  name: dataCollectionRuleName
  location: location
  tags: {
    environment: environment
    purpose: 'service-tags-monitoring'
  }
  dependsOn: [
    customTable
  ]
  properties: {
    dataCollectionEndpointId: dataCollectionEndpoint.id
    streamDeclarations: {
      '${streamName}': {
        columns: [
          {
            name: 'TimeGenerated'
            type: 'datetime'
          }
          {
            name: 'Name'
            type: 'string'
          }
          {
            name: 'Value'
            type: 'real'
          }
          {
            name: 'subscription'
            type: 'string'
          }
          {
            name: 'region'
            type: 'string'
          }
          {
            name: 'ipTagType'
            type: 'string'
          }
          {
            name: 'tag'
            type: 'string'
          }
        ]
      }
    }
    destinations: {
      logAnalytics: [
        {
          workspaceResourceId: logAnalyticsWorkspace.id
          name: 'LogAnalyticsDestination'
        }
      ]
    }
    dataFlows: [
      {
        streams: [
          streamName
        ]
        destinations: [
          'LogAnalyticsDestination'
        ]
        transformKql: 'source'
        outputStream: 'Custom-${customTableName}'
      }
    ]
  }
}

// Azure Monitor Workspace (for Prometheus metrics)
resource azureMonitorWorkspace 'Microsoft.Monitor/accounts@2023-04-03' = {
  name: workspaceName
  location: location
  tags: {
    environment: environment
    purpose: 'service-tags-monitoring'
  }
}

// User-assigned managed identity for Automation Account
resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${namePrefix}-${environment}-identity'
  location: location
  tags: {
    environment: environment
    purpose: 'service-tags-monitoring'
  }
}

// Role assignment: Reader access to the resource group for IP discovery
// Note: For cross-subscription access, additional Reader roles need to be assigned manually
// or through separate subscription-level deployments
resource readerRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, managedIdentity.id, 'Reader')
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'acdd72a7-3385-48ef-bd42-f606fba81ae7') // Reader
    principalId: managedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

// Role assignment: Monitoring Metrics Publisher for Azure Monitor (scoped to DCR)
resource monitoringMetricsPublisherRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(dataCollectionRule.id, managedIdentity.id, 'MonitoringMetricsPublisher')
  scope: dataCollectionRule
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '3913510d-42f4-4e42-8a64-420c390055eb') // Monitoring Metrics Publisher
    principalId: managedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

// Role assignment: Log Analytics Contributor for custom logs
resource logAnalyticsContributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(logAnalyticsWorkspace.id, managedIdentity.id, 'LogAnalyticsContributor')
  scope: logAnalyticsWorkspace
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '92aaf0da-9dab-42b6-94a3-d43ce8d16293') // Log Analytics Contributor
    principalId: managedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

// Azure Automation Account  
resource automationAccount 'Microsoft.Automation/automationAccounts@2023-11-01' = {
  name: '${namePrefix}-${environment}-automation'
  location: location
  tags: {
    environment: environment
    purpose: 'service-tags-monitoring'
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${managedIdentity.id}': {}
    }
  }
  properties: {
    sku: {
      name: 'Basic'
    }
    encryption: {
      keySource: 'Microsoft.Automation'
    }
  }
}

// Python runbook for service tag monitoring
resource serviceTagRunbook 'Microsoft.Automation/automationAccounts/runbooks@2023-11-01' = {
  parent: automationAccount
  name: 'ServiceTagMonitoringPython'
  location: location
  properties: {
    runbookType: 'Python3'
    logVerbose: true
    logProgress: true
    description: 'Automated service tag monitoring and metrics collection using Python'
  }
}

// Schedule for running the service tag monitoring job every 6 hours
resource serviceTagSchedule 'Microsoft.Automation/automationAccounts/schedules@2023-11-01' = {
  parent: automationAccount
  name: 'ServiceTagMonitoring-Schedule'
  properties: {
    description: 'Run service tag monitoring every 6 hours'
    startTime: scheduleStartTime // Start 30 minutes from deployment
    frequency: 'Hour'
    interval: 6
    timeZone: 'UTC'
  }
}

// Outputs
output workspaceId string = azureMonitorWorkspace.id
output workspaceName string = azureMonitorWorkspace.name
output logAnalyticsWorkspaceId string = logAnalyticsWorkspace.id
output logAnalyticsWorkspaceName string = logAnalyticsWorkspace.name
output dataCollectionEndpointUrl string = dataCollectionEndpoint.properties.logsIngestion.endpoint
output dataCollectionRuleId string = dataCollectionRule.properties.immutableId
output streamName string = streamName
output customTableName string = customTableName
output managedIdentityId string = managedIdentity.id
output managedIdentityClientId string = managedIdentity.properties.clientId
output managedIdentityPrincipalId string = managedIdentity.properties.principalId
output automationAccountName string = automationAccount.name
output runbookName string = serviceTagRunbook.name
output scheduleName string = serviceTagSchedule.name
output scheduleNextRun string = serviceTagSchedule.properties.startTime
output usageInstructions object = {
  pythonExample: 'python list-ip-addresses.py --workspace-endpoint "${dataCollectionEndpoint.properties.logsIngestion.endpoint}" --rule-id "${dataCollectionRule.properties.immutableId}" --stream-name "${streamName}"'
  powershellExample: '.\\list-ip-addresses.ps1 -WorkspaceEndpoint "${dataCollectionEndpoint.properties.logsIngestion.endpoint}" -RuleId "${dataCollectionRule.properties.immutableId}" -StreamName "${streamName}"'
  queryExample: 'Query logs using: ${customTableName} | where TimeGenerated > ago(1h) | summarize sum(Value) by subscription, region'
  automationInfo: 'Automation Account "${automationAccount.name}" created with runbook "${serviceTagRunbook.name}" scheduled to run every 6 hours starting at ${serviceTagSchedule.properties.startTime}'
}