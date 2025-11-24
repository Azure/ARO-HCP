targetScope = 'subscription'

@description('Number of resource groups to create (pool size)')
param poolSize int = 10

@description('Base name for resource groups')
param resourceGroupBaseName string = 'e2e-msi-container'

@description('Location for all resources')
param location string = deployment().location

@description('Tags to apply to all resources')
param commonTags object = {
  purpose: 'aro-hcp-e2e-msi-pool'
}

// Create N resource groups
resource resourceGroups 'Microsoft.Resources/resourceGroups@2021-04-01' = [for i in range(0, poolSize): {
  name: '${resourceGroupBaseName}-${i}'
  location: location
  tags: union(commonTags, {
    'pool-index': string(i)
  })
}]

// Create managed identities in each resource group to form the pool
module msis 'modules/cluster-identities.bicep' = [for i in range(0, poolSize): {
  name: 'msi-deployment-${i}'
  scope: resourceGroups[i]
}]

output resourceGroups array = [for i in range(0, poolSize): {
  name: resourceGroups[i].name
  location: resourceGroups[i].location
}]
