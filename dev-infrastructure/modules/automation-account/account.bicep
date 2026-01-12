param location string = resourceGroup().location

@description('Name of the automation account')
param automationAccountName string

@description('Name of the managed identity')
param automationAccountManagedIdentity string = 'automation-account-identity'

@description('Dry run flag for all the runbooks in the automation account')
param dryRun bool = false

param python3Packages array

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: automationAccountManagedIdentity
  location: location
}

resource automationAccount 'Microsoft.Automation/automationAccounts@2022-08-08' = {
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${uami.id}': {}
    }
  }
  name: automationAccountName
  location: location
  properties: {
    sku: {
      name: 'Basic'
    }
    publicNetworkAccess: false
  }
}

// Custom Python-3.10 runtime environment (editable, unlike system-generated)
resource python310CustomRuntime 'Microsoft.Automation/automationAccounts/runtimeEnvironments@2024-10-23' = {
  parent: automationAccount
  name: 'Python-3_10-Custom'
  location: location
  properties: {
    runtime: {
      language: 'Python'
      version: '3.10'
    }
    description: 'Custom Python 3.10 runtime environment with Azure packages'
  }
}

// Add packages to custom Python-3.10 runtime environment
resource python310CustomPackages 'Microsoft.Automation/automationAccounts/runtimeEnvironments/packages@2024-10-23' = [
  for pkg in python3Packages: {
    parent: python310CustomRuntime
    name: pkg.name
    properties: {
      contentLink: {
        contentHash: {
          algorithm: pkg.algorithm
          value: pkg.hash
        }
        uri: pkg.url
      }
    }
  }
]

output customRuntimeName string = python310CustomRuntime.name

resource emailActionGroup 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'singleEmailAction'
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${uami.id}': {}
    }
  }
  properties: {
    emailReceivers: [
      {
        emailAddress: 'aro-hcp-service-lifecycle-team@redhat.com'
        name: 'aro-hcp-service-lifecycle-team'
        useCommonAlertSchema: false
      }
    ]
    enabled: true
    groupShortName: 'email'
  }
  tags: {
    app: 'cleanup-resources'
  }
}

resource failedJobAlertRule 'Microsoft.Insights/scheduledQueryRules@2023-03-15-preview' = {
  name: 'failed jobs alert'
  location: location
  properties: {
    actions: {
      actionGroups: [
        emailActionGroup.id
      ]
    }
    autoMitigate: false
    criteria: {
      allOf: [
        {
          dimensions: []
          failingPeriods: {
            minFailingPeriodsToAlert: 1
            numberOfEvaluationPeriods: 1
          }
          operator: 'GreaterThan'
          query: '''AzureDiagnostics
| where ResourceProvider == "MICROSOFT.AUTOMATION"
    and Category == "JobLogs"
    and (ResultType == "Failed")
| project TimeGenerated, RunbookName_s, ResultType, _ResourceId, JobId_g
'''
          resourceIdColumn: ''
          threshold: 0
          timeAggregation: 'Count'
        }
      ]
    }
    description: 'Alert configured to trigger when there are Jobs that failed'
    displayName: 'failed jobs alert'
    enabled: true
    evaluationFrequency: 'P1D'
    scopes: [automationAccount.id]
    severity: 2
    targetResourceTypes: ['Microsoft.Automation/automationAccounts']
    windowSize: 'P1D'
  }
}

resource automationAccountVariable_SubscriptionId 'Microsoft.Automation/automationAccounts/variables@2024-10-23' = {
  parent: automationAccount
  name: 'subscription_id'
  properties: {
    description: 'The subscription Id of the automation account'
    isEncrypted: false
    value: '"${subscription().subscriptionId}"'
  }
}

resource automationAccountVariable_ClientId 'Microsoft.Automation/automationAccounts/variables@2024-10-23' = {
  parent: automationAccount
  name: 'client_id'
  properties: {
    description: 'The subscription Id of the automation account'
    isEncrypted: false
    value: '"${uami.properties.clientId}"'
  }
}

resource automationAccountVariable_DryRun 'Microsoft.Automation/automationAccounts/variables@2024-10-23' = {
  parent: automationAccount
  name: 'dry_run'
  properties: {
    description: 'The dry run flag for the runbook'
    isEncrypted: false
    value: '"${dryRun}"'
  }
}

output name string = automationAccount.name
output managedIdentityPrincipalId string = uami.properties.principalId
