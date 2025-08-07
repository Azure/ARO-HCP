@description('If set to true, the cluster will not be deleted automatically after few days.')
param persistTagValue bool

@description('Network Security Group Name')
param customerNsgName string = 'customer-nsg'

@description('Virtual Network Name')
param customerVnetName string = 'customer-vnet'

@description('Subnet Name')
param customerVnetSubnetName string = 'customer-subnet-1'

var addressPrefix = '10.0.0.0/16'
var subnetPrefix = '10.0.0.0/24'

resource customerNsg 'Microsoft.Network/networkSecurityGroups@2023-05-01' = {
  name: customerNsgName
  location: resourceGroup().location
  tags: {
    persist: persistTagValue
  }
}

resource customerVnet 'Microsoft.Network/virtualNetworks@2023-05-01' = {
  name: customerVnetName
  location: resourceGroup().location
  tags: {
    persist: persistTagValue
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

//
// outputs
// 

@description('Network Security Group Name')
output nsgName string = customerNsgName

@description('Virtual Network Name')
output vnetName string = customerVnetName

@description('Subnet Name')
output vnetSubnetName string = customerVnetSubnetName
