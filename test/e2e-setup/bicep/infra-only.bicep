@description('If set to true, the cluster will not be deleted automatically after few days.')
param persistTagValue bool = false

@description('Name of the hypershift cluster')
param clusterName string

module customerInfra 'modules/customer-infra.bicep' = {
  name: 'customerInfra'
  params: {
    persistTagValue: persistTagValue
  }
}

module managedIdentities 'modules/managed-identities.bicep' = {
  name: 'managedIdentities'
  params: {
    clusterName: clusterName
  }
}

// passing details about managed identities via the outputs of the main
// bicep file directly for this to be more visible
output userAssignedIdentitiesValue object = managedIdentities.outputs.userAssignedIdentitiesValue
output identityValue object = managedIdentities.outputs.identityValue
