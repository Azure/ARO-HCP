@description('Network Security Group Name')
param customerNsgName string

@description('Virtual Network Name')
param customerVnetName string

@description('Subnet Name')
param customerVnetSubnetName string

var addressPrefix = '10.0.0.0/16'
var subnetPrefix = '10.0.0.0/24'

resource customerNsg 'Microsoft.Network/networkSecurityGroups@2023-05-01' = {
  name: customerNsgName
  location: resourceGroup().location
  tags: {
    persist: 'true'
  }
}

resource customerVnet 'Microsoft.Network/virtualNetworks@2023-05-01' = {
  name: customerVnetName
  location: resourceGroup().location
  tags: {
    persist: 'true'
  }
  properties: {
    addressSpace: {
      addressPrefixes: [
        addressPrefix
      ]
    }
    subnets: [
      {
        name: customerVnetSubnetName
        properties: {
          addressPrefix: subnetPrefix
          networkSecurityGroup: {
            id: customerNsg.id
          }
        }
      }
    ]
  }
}

output subnetId string = customerVnet.properties.subnets[0].id
output networkSecurityGroupId string = customerNsg.id
