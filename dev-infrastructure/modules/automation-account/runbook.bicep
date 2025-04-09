param location string = resourceGroup().location

@description('Name of the automation account')
param automationAccountName string

@description('Name of the runbook to create')
param runbookName string

@description('Object, expecting `ref` and `path` properties')
param rubookScript object

@description('Version of this runbook')
param runbookVersion string

@description('Description of this runbook')
param runbookDescription string

@description('Type of this runbook')
param runbookType string

@description('Name of the schedule for this runbook(if empty, no schedule will be created)')
param scheduleName string = ''

@description('Schedule frequency (e.g., Hour, Day, Week)')
param frequency string = 'Day'

@description('Interval for the schedule execution')
param interval int = 1

@description('Start time for the scheduled execution (12:00 AM the next day)')
param startTime string = '${substring(dateTimeAdd(utcNow(), 'P1D'), 0, 10)}T00:00:00Z'

@description('Name of the managed identity')
param identityName string = 'hcp-dev-automation'

resource automationAccount 'Microsoft.Automation/automationAccounts@2022-08-08' existing = {
  name: automationAccountName
}

var rubookScriptUrl = format(
  'https://raw.githubusercontent.com/Azure/ARO-HCP/{0}/{1}',
  rubookScript.ref,
  rubookScript.path
)

resource accountRunbook 'Microsoft.Automation/automationAccounts/runbooks@2022-08-08' = {
  name: '${automationAccountName}_${runbookName}'
  location: location
  parent: automationAccount
  properties: {
    description: runbookDescription
    runbookType: runbookType
    logProgress: false
    logVerbose: true
    publishContentLink: {
      uri: rubookScriptUrl
      version: runbookVersion
    }
  }
}

// Create the Schedule
resource runbookSchedule 'Microsoft.Automation/automationAccounts/schedules@2022-08-08' = if (!empty(scheduleName)) {
  name: '${automationAccountName}_${scheduleName}'
  parent: automationAccount
  properties: {
    frequency: frequency
    interval: interval
    startTime: startTime
    timeZone: 'UTC'
  }
}

// Link Schedule to Runbook
resource registerScheduledRunbook 'Microsoft.Resources/deploymentScripts@2023-08-01' = if (!empty(scheduleName)) {
  name: 'registerScheduledRunbook_${uniqueString(runbookName, scheduleName)}'
  location: resourceGroup().location
  kind: 'AzurePowerShell'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${subscription().id}/resourceGroups/${resourceGroup().name}/providers/Microsoft.ManagedIdentity/userAssignedIdentities/${identityName}': {}
    }
  }
  properties: {
    azPowerShellVersion: '12.0.0'
    scriptContent: loadTextContent('../../scripts/register-scheduledrunbook.ps1')
    arguments: '-ResourceGroupName ${resourceGroup().name} -AutomationAccountName ${automationAccountName} -RunbookName ${accountRunbook.name} -ScheduleName ${runbookSchedule.name}'
    retentionInterval: 'P1D'
    cleanupPreference: 'OnSuccess'
    timeout: 'PT30M'
  }
}
