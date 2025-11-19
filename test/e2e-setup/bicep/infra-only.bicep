@description('If set to true, the cluster will not be deleted automatically after few days.')
param persistTagValue bool = false

@description('Array of MSI resource IDs from leased pool')
param msiIds array

module customerInfra 'modules/customer-infra.bicep' = {
  name: 'customerInfra'
  params: {
    persistTagValue: persistTagValue
  }
}

module managedIdentities 'modules/managed-identities.bicep' = {
  name: 'managedIdentities'
  params: {
    msiIds: msiIds
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
