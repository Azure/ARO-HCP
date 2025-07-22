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

module AroHcpCluster 'modules/cluster.bicep' = {
  name: 'cluster'
  params: {
    clusterName: clusterName
    subnetId: customerInfra.outputs.subnetId
    networkSecurityGroupId: customerInfra.outputs.networkSecurityGroupId
    userAssignedIdentitiesValue: managedIdentities.outputs.userAssignedIdentitiesValue
    identityValue: managedIdentities.outputs.identityValue
  }
}

module AroHcpNodePool 'modules/nodepool.bicep' = {
  name: 'nodepool-1'
  params: {
    clusterName: clusterName
    nodePoolName: 'nodepool-1'
  }
}
