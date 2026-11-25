targetScope = 'resourceGroup'

@description('If set to true, the cluster will not be deleted automatically after few days.')
param persistTagValue bool = false

@description('Managed identities to use')
param identities object

module customerInfra 'modules/customer-infra.bicep' = {
  name: 'customerInfra'
  params: {
    persistTagValue: persistTagValue
  }
}

module managedIdentities 'modules/managed-identities.bicep' = {
  name: 'managedIdentities'
  scope: subscription()
  params: {
    msiResourceGroupName: identities.resourceGroup
    clusterResourceGroupName: resourceGroup().name
    identities: identities.identities
    vnetName: customerInfra.outputs.vnetName
    subnetName: customerInfra.outputs.vnetSubnetName
    nsgName: customerInfra.outputs.nsgName
    keyVaultName: customerInfra.outputs.keyVaultName
  }
}

// passing details about managed identities via the outputs of the main
// bicep file directly for this to be more visible
output userAssignedIdentitiesValue object = managedIdentities.outputs.userAssignedIdentitiesValue
output identityValue object = managedIdentities.outputs.identityValue
