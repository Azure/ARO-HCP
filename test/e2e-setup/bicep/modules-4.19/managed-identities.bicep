targetScope = 'subscription'

@description('Resource group name where identities are located')
param msiResourceGroupName string

@description('HCP cluster RG name')
param clusterResourceGroupName string

@description('If true, use the pre-created MSI pool in msiResourceGroupName; if false, create MSIs in the cluster resource group')
param useMsiPool bool = true

type ManagedIdentities = {
  clusterApiAzureMiName: string
  controlPlaneMiName: string
  cloudControllerManagerMiName: string
  ingressMiName: string
  diskCsiDriverMiName: string
  fileCsiDriverMiName: string
  imageRegistryMiName: string
  cloudNetworkConfigMiName: string
  kmsMiName: string
  dpDiskCsiDriverMiName: string
  dpFileCsiDriverMiName: string
  dpImageRegistryMiName: string
  serviceManagedIdentityName: string
}

@description('MSI identities in the pool')
param identities ManagedIdentities

@description('The Network security group name for the HCP cluster resources')
param nsgName string

@description('The virtual network name for the HCP cluster resources')
param vnetName string

@description('The subnet name for deploying HCP cluster resources')
param subnetName string

@description('The KeyVault name that contains the etcd encryption key')
param keyVaultName string

// P O O L E D   M O D E
module pooledNonMsiScopedAssignments 'non-msi-scoped-assignments.bicep' = if (useMsiPool) {
  name: 'pooledNonMsiScopedAssignments'
  scope: resourceGroup(clusterResourceGroupName)
  params: {
    resourceGroupName: msiResourceGroupName
    identities: identities
    vnetName: vnetName
    subnetName: subnetName
    nsgName: nsgName
    keyVaultName: keyVaultName
  }
}

module pooledMsiScopedAssignments 'msi-scoped-assignments.bicep' = if (useMsiPool) {
  name: 'pooledMsiScopedAssignments'
  scope: resourceGroup(msiResourceGroupName)
  params: {
    identities: identities
  }
}

// N O N   P O O L E D   M O D E
// Create identities in the cluster resource group for environments without an MSI pool available.
module clusterIdentities 'cluster-identities.bicep' = if (!useMsiPool) {
  name: 'clusterIdentities'
  scope: resourceGroup(clusterResourceGroupName)
  params: {
    identities: identities
  }
}

module clusterNonMsiScopedAssignments 'non-msi-scoped-assignments.bicep' = if (!useMsiPool) {
  name: 'clusterNonMsiScopedAssignments'
  scope: resourceGroup(clusterResourceGroupName)
  params: {
    // In cluster mode, the identities live in the cluster resource group.
    resourceGroupName: clusterResourceGroupName
    identities: clusterIdentities.outputs.msiIdentities
    vnetName: vnetName
    subnetName: subnetName
    nsgName: nsgName
    keyVaultName: keyVaultName
  }
}

module clusterMsiScopedAssignments 'msi-scoped-assignments.bicep' = if (!useMsiPool) {
  name: 'clusterMsiScopedAssignments'
  scope: resourceGroup(clusterResourceGroupName)
  params: {
    identities: clusterIdentities.outputs.msiIdentities
  }
}

output userAssignedIdentitiesValue object = useMsiPool
  ? pooledNonMsiScopedAssignments.outputs.userAssignedIdentitiesValue
  : clusterNonMsiScopedAssignments.outputs.userAssignedIdentitiesValue

output identityValue object = useMsiPool
  ? pooledNonMsiScopedAssignments.outputs.identityValue
  : clusterNonMsiScopedAssignments.outputs.identityValue