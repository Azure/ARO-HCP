param vnetId string /* TODO: fill in correct type */

@description('Name of the VNET that will contain the AKS cluster and related resources.')
param vnetName string

resource dev_vpn_vnet_peering_vnet 'Microsoft.Network/virtualNetworks/virtualNetworkPeerings@2023-04-01' = {
  name: 'dev-vpn-vnet/peering-${vnetName}'
  properties: {
    allowVirtualNetworkAccess: true
    allowForwardedTraffic: true
    allowGatewayTransit: true
    useRemoteGateways: false
    remoteVirtualNetwork: {
      id: vnetId
    }
  }
}
