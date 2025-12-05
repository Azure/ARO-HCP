targetScope = 'resourceGroup'

@description('If set to true, the cluster will not be deleted automatically after few days.')
param persistTagValue bool = false

@description('Name of the hypershift cluster')
param clusterName string

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

module AroHcpCluster 'modules/cluster.bicep' = {
  name: 'cluster'
  params: {
    clusterName: clusterName
    vnetName: customerInfra.outputs.vnetName
    subnetName: customerInfra.outputs.vnetSubnetName
    nsgName: customerInfra.outputs.nsgName
    userAssignedIdentitiesValue: managedIdentities.outputs.userAssignedIdentitiesValue
    identityValue: managedIdentities.outputs.identityValue
    keyVaultName: customerInfra.outputs.keyVaultName
    etcdEncryptionKeyName: customerInfra.outputs.etcdEncryptionKeyName
  }
}

module AroHcpNodePool 'modules/nodepool.bicep' = {
  name: 'nodepool-1'
  params: {
    clusterName: AroHcpCluster.outputs.name
    nodePoolName: 'nodepool-1'
  }
}
