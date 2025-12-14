targetScope = 'resourceGroup'

@description('If set to true, the cluster will not be deleted automatically after few days.')
param persistTagValue bool = false

@description('Name of the hypershift cluster')
param clusterName string

@description('Managed identities to use')
param identities object

@description('When true, use the pre-created MSI pool instead of creating identities in the cluster resource group')
param usePooledIdentities bool = false

@description('ControlPlane OpenShift Version ID')
param openshiftControlPlaneVersionId string = '4.19'

@description('NodePool OpenShift Version ID')
param openshiftNodePoolVersionId string = '4.19.7'

module customerInfra 'modules/customer-infra.bicep' = {
  name: 'customerInfra'
  params: {
    persistTagValue: persistTagValue
  }
}

module managedIdentities 'modules/managed-identities.bicep' = {
  name: 'mi-${resourceGroup().name}'
  scope: subscription()
  params: {
    msiResourceGroupName: identities.resourceGroup
    clusterResourceGroupName: resourceGroup().name
    identities: identities.identities
    useMsiPool: usePooledIdentities
    vnetName: customerInfra.outputs.vnetName
    subnetName: customerInfra.outputs.vnetSubnetName
    nsgName: customerInfra.outputs.nsgName
    keyVaultName: customerInfra.outputs.keyVaultName
  }
}

module AroHcpCluster 'modules/cluster.bicep' = {
  name: 'cluster'
  params: {
    openshiftVersionId: openshiftControlPlaneVersionId
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
    openshiftVersionId: openshiftNodePoolVersionId
    clusterName: AroHcpCluster.outputs.name
    nodePoolName: 'nodepool-1'
  }
}
