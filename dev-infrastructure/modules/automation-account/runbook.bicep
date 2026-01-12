param location string = resourceGroup().location

@description('Name of the automation account')
param automationAccountName string

@description('Name of the runbook to create')
param runbookName string

@description('Object, expecting `ref` and `path` properties')
param runbookScript object

@description('Version of this runbook')
param runbookVersion string

@description('Description of this runbook')
param runbookDescription string

@description('Type of this runbook')
param runbookType string

@description('Runtime environment for this runbook (e.g., Python-3.10, PowerShell-7.2)')
param runtimeEnvironment string = ''

@description('Verbose flag for this runbook')
param verbose bool = false

@description('Name of the schedule for this runbook(if empty, no schedule will be created)')
param scheduleName string = ''

@description('Schedule frequency (e.g., Hour, Day, Week)')
param frequency string = 'Day'

@description('Interval for the schedule execution')
param interval int = 1

@description('Start time for the scheduled execution (12:00 AM the next day)')
param startTime string = '${substring(dateTimeAdd(utcNow(), 'P1D'), 0, 10)}T00:00:00Z'

@description('Deployment timestamp to ensure unique jobSchedule names')
param deploymentTime string = utcNow()

resource automationAccount 'Microsoft.Automation/automationAccounts@2024-10-23' existing = {
  name: automationAccountName
}

var runbookScriptUrl = format(
  'https://raw.githubusercontent.com/Azure/ARO-HCP/{0}/{1}',
  runbookScript.ref,
  runbookScript.path
)

resource accountRunbook 'Microsoft.Automation/automationAccounts/runbooks@2024-10-23' = {
  name: '${automationAccount.name}_${runbookName}'
  location: location
  parent: automationAccount
  properties: {
    description: runbookDescription
    runbookType: runbookType
    runtimeEnvironment: !empty(runtimeEnvironment) ? runtimeEnvironment : null
    logProgress: false
    logVerbose: verbose
    publishContentLink: {
      uri: runbookScriptUrl
      version: runbookVersion
    }
  }
}

// Create the Schedule
resource runbookSchedule 'Microsoft.Automation/automationAccounts/schedules@2024-10-23' = if (!empty(scheduleName)) {
  name: '${automationAccount.name}_${scheduleName}'
  parent: automationAccount
  properties: {
    frequency: frequency
    interval: interval
    startTime: startTime
    timeZone: 'UTC'
  }
}

resource runbookJobSchedule 'Microsoft.Automation/automationAccounts/jobSchedules@2024-10-23' = if (!empty(scheduleName)) {
  // A nondeterministic name for the job schedule avoids redeployment conflicts,
  // together with the Azure CLI's complete mode to remove orphaned job schedules.
  // More information at https://red.ht/3TSWBVi
  name: guid(accountRunbook.name, runbookSchedule.name, deploymentTime)
  parent: automationAccount
  properties: {
    runbook: {
      name: accountRunbook.name
    }
    schedule: {
      name: runbookSchedule.name
    }
  }
}
