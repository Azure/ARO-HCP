@description('Name of the hypershift cluster')
param clusterName string
param location string

var randomSuffix = toLower(uniqueString(clusterName))

// Control plane identities
resource clusterApiAzureMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cluster-api-azure-${randomSuffix}'
  location: location
}

resource controlPlaneMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-control-plane-${randomSuffix}'
  location: location
}

resource cloudControllerManagerMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cloud-controller-manager-${randomSuffix}'
  location: location
}

resource ingressMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-ingress-${randomSuffix}'
  location: location
}

resource diskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-disk-csi-driver-${randomSuffix}'
  location: location
}

resource fileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-file-csi-driver-${randomSuffix}'
  location: location
}

resource imageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-image-registry-${randomSuffix}'
  location: location
}

resource cloudNetworkConfigMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cloud-network-config-${randomSuffix}'
  location: location
}

// Data plane identities
resource dpDiskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-disk-csi-driver-${randomSuffix}'
  location: location
}

resource dpFileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-file-csi-driver-${randomSuffix}'
  location: location
}

resource dpImageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-image-registry-${randomSuffix}'
  location: location
}

// Service managed identity
resource serviceManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-service-managed-identity-${randomSuffix}'
  location: location
}

// Outputs for control plane identities
output clusterApiAzureMiId string = clusterApiAzureMi.id
output controlPlaneMiId string = controlPlaneMi.id
output cloudControllerManagerMiId string = cloudControllerManagerMi.id
output ingressMiId string = ingressMi.id
output diskCsiDriverMiId string = diskCsiDriverMi.id
output fileCsiDriverMiId string = fileCsiDriverMi.id
output imageRegistryMiId string = imageRegistryMi.id
output cloudNetworkConfigMiId string = cloudNetworkConfigMi.id

// Outputs for data plane identities
output dpDiskCsiDriverMiId string = dpDiskCsiDriverMi.id
output dpFileCsiDriverMiId string = dpFileCsiDriverMi.id
output dpImageRegistryMiId string = dpImageRegistryMi.id

// Output for service managed identity
output serviceManagedIdentityId string = serviceManagedIdentity.id
