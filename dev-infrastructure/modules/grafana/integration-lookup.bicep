// This deployment script is used to lookup the Azure Monitor workspace ids for a given Grafana instance.
// If the given Grafana instance is not found, the script will return an empty array.

param grafanaName string

param deploymentScriptIdentityId string

param location string

param _now string = utcNow('F')

// Azure Managed Grafana Workspace Contributor: Can manage Azure Managed Grafana resources, without providing access to the workspaces themselves.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/monitor#azure-managed-grafana-workspace-contributor
var grafanaContributor = '5c2d7e57-b7c2-4d8a-be4f-82afa42c6e95'

resource contributorRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, deploymentScriptIdentityId, grafanaContributor)
  scope: resourceGroup()
  properties: {
    principalId: reference(deploymentScriptIdentityId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', grafanaContributor)
  }
}

resource grafanaIntegrationsLookup 'Microsoft.Resources/deploymentScripts@2020-10-01' = {
  name: 'grafana-workspace-lookup-script'
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${deploymentScriptIdentityId}': {}
    }
  }
  kind: 'AzurePowerShell'
  properties: {
    azPowerShellVersion: '12.0.0'
    timeout: 'PT10M'
    retentionInterval: 'P1D'
    cleanupPreference: 'Always'
    scriptContent: loadTextContent('grafanaIntegrationsLookup.ps1')
    arguments: '-grafanaResourceGroup ${resourceGroup().name} -grafanaName ${grafanaName}'
    forceUpdateTag: _now
  }
  dependsOn: [
    contributorRole
  ]
}

output azureMonitorWorkspaceIds array = grafanaIntegrationsLookup.properties.outputs.workspaceIds ?? []
