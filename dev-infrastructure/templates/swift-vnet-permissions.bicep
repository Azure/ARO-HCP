@description('The global deployment identity resource ID, used when no Swift identity override is configured')
param deploymentMsiId string

@description('Optional Swift-registered managed identity resource ID; empty uses the global deployment identity')
param swiftVnetIdentityResourceId string = ''

var vnetIdentityResourceId = empty(swiftVnetIdentityResourceId) ? deploymentMsiId : swiftVnetIdentityResourceId

module swiftVnetRbac '../modules/network/vnet-rbac.bicep' = {
  name: 'swift-vnet-rbac'
  params: {
    deploymentMsiId: vnetIdentityResourceId
  }
}

output swiftVnetMSIId string = vnetIdentityResourceId
