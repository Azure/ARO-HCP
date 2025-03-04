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
