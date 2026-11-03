@description('Whether to create the Kusto cost reporting identity')
param enabled bool

@description('Name of the Kusto cost reporting managed identity')
param identityName string

@description('Azure Region Location')
param location string = resourceGroup().location

// One identity per environment, used by the KustoCost reporter. It gets Cost
// Management Reader and Reader on the global subscription, which hosts the
// Kusto clusters; database-scoped Viewer is granted by the geography pipeline.
resource costReporterIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = if (enabled) {
  name: identityName
  location: location
}

module subscriptionRoles '../modules/logs/kusto/cost-reporter-subscription-roles.bicep' = if (enabled) {
  name: 'kusto-cost-reporter-roles'
  scope: subscription()
  params: {
    principalId: costReporterIdentity!.properties.principalId
  }
}

output principalId string = enabled ? costReporterIdentity!.properties.principalId : ''
output clientId string = enabled ? costReporterIdentity!.properties.clientId : ''
