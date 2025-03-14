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

@description('Start time for the scheduled execution (must be at least 5 min from deployment time)')
param startTime string = dateTimeAdd(utcNow(), 'PT15M')

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
resource runbookSchedule 'Microsoft.Automation/automationAccounts/schedules@2022-08-08' = {
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
resource jobSchedule 'Microsoft.Automation/automationAccounts/jobSchedules@2022-08-08' = {
  name: guid(automationAccountName, runbookName, scheduleName)
  parent: automationAccount
  properties: {
    schedule: {
      name: runbookSchedule.name
    }
    runbook: {
      name: accountRunbook.name
    }
  }
}
