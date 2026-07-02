@description('The resource ID of the user-assigned managed identity that manages the Swift VNet')
param deploymentMsiId string

@description('Whether Swift VNet is enabled. Gates the Swift VNet RBAC role assignments so they are only created when Swift is in use.')
param enableSwift bool

module swiftVnetRbac '../modules/network/vnet-rbac.bicep' = if (enableSwift) {
  name: 'swift-vnet-rbac'
  params: {
    deploymentMsiId: deploymentMsiId
  }
}
