@description('The name of the storage account for deployment scripts')
param deploymentScriptStorageAccountName string

@description('The name of the VNet for deployment scripts')
param deploymentScriptVnetName string = 'deployment-scripts-vnet'

resource deploymentScriptVnet 'Microsoft.Network/virtualNetworks@2024-05-01' existing = {
  name: deploymentScriptVnetName

  resource subnet 'subnets' existing = {
    name: 'deployment-scripts'
  }
}

output deploymentScriptStorageAccountName string = deploymentScriptStorageAccountName
output deploymentScriptSubnetId string = deploymentScriptVnet::subnet.id
