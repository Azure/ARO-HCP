@description('Index of the resource group in the pool')
param resourceGroupIndex int

@description('Number of MSIs to create')
param msiCount int

@description('Location for all resources')
param location string

@description('Base name for MSI resources')
param baseName string

// Create M managed identities in this resource group
resource managedIdentities 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = [for j in range(0, msiCount): {
  name: '${baseName}-${resourceGroupIndex}-msi-${j}'
  location: location
  tags: {
    'pool-rg-index': string(resourceGroupIndex)
    'msi-index': string(j)
    'aro-hcp-e2e': 'msi-pool'
  }
}]

output msiIds array = [for j in range(0, msiCount): managedIdentities[j].id]
output msiNames array = [for j in range(0, msiCount): managedIdentities[j].name]