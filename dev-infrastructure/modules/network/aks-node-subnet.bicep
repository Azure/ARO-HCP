@description('The VNET that should be tagged')
param vnetName string

@description('The name of the subnet')
param subnetName string

@description('The prefix for the subnet')
param subnetPrefix string

@description('The resource ID of the network security group for the subnet')
param subnetNSGId string

resource vnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = {
  name: vnetName
}

resource subnet 'Microsoft.Network/virtualNetworks/subnets@2023-11-01' = {
  parent: vnet
  name: subnetName
  properties: {
    addressPrefix: subnetPrefix
    privateEndpointNetworkPolicies: 'Disabled'
    serviceEndpoints: [
      {
        service: 'Microsoft.AzureCosmosDB'
      }
      {
        service: 'Microsoft.ContainerRegistry'
      }
      {
        service: 'Microsoft.Storage'
      }
      {
        service: 'Microsoft.KeyVault'
      }
    ]
    defaultOutboundAccess: false
    networkSecurityGroup: {
      id: subnetNSGId
    }
  }
}

output subnetId string = subnet.id
