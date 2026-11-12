@description('The Swift VNet identity resource ID resolved by the global output step')
param swiftVnetIdentityResourceId string

@description('The configured Swift VNet identity override; empty preserves the global launcher permissions')
param configuredSwiftVnetIdentityResourceId string = ''

module swiftVnetRbac '../modules/network/vnet-rbac.bicep' = {
  name: 'swift-vnet-rbac'
  params: {
    deploymentMsiId: swiftVnetIdentityResourceId
  }
}

// Azure Container Instances Contributor Role, scoped to the management resource group.
var containerInstancesContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '5d977122-f97e-4b4d-a52f-6b43003ddb4d'
)

// Evaluate the override in ARM, not against resolve-mode configuration placeholders.
resource swiftVnetContainerContributor 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(configuredSwiftVnetIdentityResourceId)) {
  scope: resourceGroup()
  name: guid(swiftVnetIdentityResourceId, containerInstancesContributorRoleId, resourceGroup().id)
  properties: {
    roleDefinitionId: containerInstancesContributorRoleId
    principalId: reference(swiftVnetIdentityResourceId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
  }
}

output swiftVnetMSIId string = swiftVnetIdentityResourceId
