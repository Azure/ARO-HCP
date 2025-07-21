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

module ManagedIdentities 'modules/managed-identities.bicep' = {
  name: 'ManagedIdentities'
  params: {
    clusterName: clusterName
  }
}
