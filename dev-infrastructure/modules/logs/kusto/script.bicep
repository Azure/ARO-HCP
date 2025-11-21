@description('Name of the Kusto (ADX) cluster hosting the database.')
param kustoName string

@description('Name of the target Kusto database where scripts will run.')
param databaseName string

@description('Name suffix for the script resource; full resource name becomes <kustoName>/<databaseName>/<scriptName>-script')
param scriptName string = 'custom'

@description('Arbitrary Kusto control command script content to execute (secure).')
@secure()
param scriptContent string

@description('principalPermissionsAction to apply (e.g. RemovePermissionOnScriptCompletion, RetainPermissionOnScriptCompletion).')
param principalPermissionsAction string = 'RetainPermissionOnScriptCompletion'

@description('Whether to continue on errors within the script.')
param continueOnErrors bool = false

resource script 'Microsoft.Kusto/clusters/databases/scripts@2024-04-13' = {
  name: '${kustoName}/${databaseName}/${scriptName}-script'
  properties: {
    principalPermissionsAction: principalPermissionsAction
    scriptContent: scriptContent
    continueOnErrors: continueOnErrors
  }
}

@description('Created script resource name.')
output scriptResourceName string = script.name
