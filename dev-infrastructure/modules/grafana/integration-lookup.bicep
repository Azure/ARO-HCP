// This deployment script is used to lookup the Azure Monitor workspace ids for a given Grafana instance.
// If the given Grafana instance is not found, the script will return an empty array.

param grafanaName string

param deploymentScriptIdentityId string

param _now string = utcNow('F')

resource grafanaIntegrationsLookup 'Microsoft.Resources/deploymentScripts@2020-10-01' = {
  name: 'grafana-workspace-lookup-script'
  location: resourceGroup().location
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
}

output azureMonitorWorkspaceIds array = grafanaIntegrationsLookup.properties.outputs.workspaceIds ?? []
