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
  persist: 'true'
}

// Well-known managed identity names used by the test framework (NewDefaultIdentities).
var identities = {
  clusterApiAzureMiName: 'cluster-api-azure'
  controlPlaneMiName: 'control-plane'
  cloudControllerManagerMiName: 'cloud-controller-manager'
  ingressMiName: 'ingress'
  diskCsiDriverMiName: 'disk-csi-driver'
  fileCsiDriverMiName: 'file-csi-driver'
  imageRegistryMiName: 'image-registry'
  cloudNetworkConfigMiName: 'cloud-network-config'
  kmsMiName: 'kms'
  dpDiskCsiDriverMiName: 'dp-disk-csi-driver'
  dpFileCsiDriverMiName: 'dp-file-csi-driver'
  dpImageRegistryMiName: 'dp-image-registry'
  serviceManagedIdentityName: 'service'
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
  params: {
    identities: identities
  }
}]

output resourceGroups array = [for i in range(0, poolSize): {
  name: resourceGroups[i].name
  location: resourceGroups[i].location
}]
