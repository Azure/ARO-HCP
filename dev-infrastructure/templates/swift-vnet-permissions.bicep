@description('The resource ID of the user-assigned managed identity that manages the Swift VNet')
param deploymentMsiId string

module swiftVnetRbac '../modules/network/vnet-rbac.bicep' = {
  name: 'swift-vnet-rbac'
  params: {
    deploymentMsiId: deploymentMsiId
  }
}
