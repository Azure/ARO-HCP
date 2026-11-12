@description('Name of the Kusto cost reporting managed identity')
param identityName string

@description('Resource group of the Kusto cost reporting managed identity (same subscription as this deployment)')
param identityResourceGroup string

@description('Name of the Kusto cluster to grant access on')
param kustoName string

@description('Databases the identity can read')
param databaseNames array

resource costReporterIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identityName
  scope: resourceGroup(identityResourceGroup)
}

// Database-scoped Viewer only; the reporter needs no cluster-wide access.
module grantViewer '../modules/logs/kusto/grant-access.bicep' = [
  for databaseName in databaseNames: {
    name: 'kusto-cost-reporter-${databaseName}'
    params: {
      readAccessPrincipalIds: [costReporterIdentity.properties.principalId]
      databaseName: databaseName
      kustoName: kustoName
    }
  }
]
