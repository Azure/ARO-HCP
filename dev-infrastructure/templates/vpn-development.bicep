param publicIPAddressSkuName string = 'Standard'
param publicIPAddressAllocationMethod string = 'Static'
param vpnCACertificate string = ''
param location string = resourceGroup().location

resource dev_vpn_pip 'Microsoft.Network/publicIPAddresses@2023-04-01' = {
  name: 'dev-vpn-pip'
  location: location
  sku: {
    name: publicIPAddressSkuName
  }
  properties: {
    publicIPAllocationMethod: publicIPAddressAllocationMethod
  }
}

resource dev_vpn_vnet 'Microsoft.Network/virtualNetworks@2023-04-01' = {
  name: 'dev-vpn-vnet'
  location: location
  properties: {
    addressSpace: {
      addressPrefixes: [
        '10.2.0.0/20'
      ]
    }
    subnets: [
      {
        properties: {
          addressPrefix: '10.2.0.0/24'
        }
        name: 'GatewaySubnet'
      }
      ]
  }
}

resource dev_vpn 'Microsoft.Network/virtualNetworkGateways@2023-04-01' = {
  name: 'dev-vpn'
  location: location
  properties: {
    ipConfigurations: [
      {
        properties: {
          subnet: {
            id: resourceId('Microsoft.Network/virtualNetworks/subnets', 'dev-vpn-vnet', 'GatewaySubnet')
          }
          publicIPAddress: {
            id: dev_vpn_pip.id
          }
        }
        name: 'default'
      }
    ]
    vpnType: 'RouteBased'
    sku: {
      name: 'VpnGw1'
      tier: 'VpnGw1'
    }
    vpnClientConfiguration: {
      vpnClientAddressPool: {
        addressPrefixes: [
          '192.168.255.0/24'
        ]
      }
      vpnClientRootCertificates: [
        {
          properties: {
            publicCertData: vpnCACertificate
          }
          name: 'dev-vpn-ca'
        }
      ]
      vpnClientProtocols: [
        'OpenVPN'
      ]
    }
  }
  dependsOn: [

    dev_vpn_vnet
  ]
}
