param location string = resourceGroup().location

@description('Name of the automation account')
param automationAccountName string

@description('Name of the managed identity')
param automationAccountManagedIdentity string = 'automation-account-identity'

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

resource python3Package 'Microsoft.Automation/automationAccounts/python3Packages@2023-11-01' = [
  for pkg in python3Packages: {
    parent: automationAccount
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

output automationAccountManagedIdentityId string = uami.properties.principalId
