@description('The Swift VNet identity resource ID resolved by the global output step')
param swiftVnetIdentityResourceId string

module swiftVnetRbac '../modules/network/vnet-rbac.bicep' = {
  name: 'swift-vnet-rbac'
  params: {
    deploymentMsiId: swiftVnetIdentityResourceId
  }
}

output swiftVnetMSIId string = swiftVnetIdentityResourceId
