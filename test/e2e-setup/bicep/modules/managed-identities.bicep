@description('Name of the hypershift cluster')
param clusterName string

var randomSuffix = toLower(uniqueString(resourceGroup().id))

// Control plane identities

resource clusterApiAzureMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cluster-api-azure-${randomSuffix}'
  location: resourceGroup().location
}

resource controlPlaneMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-control-plane-${randomSuffix}'
  location: resourceGroup().location
}

resource cloudControllerManagerMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cloud-controller-manager-${randomSuffix}'
  location: resourceGroup().location
}

resource ingressMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-ingress-${randomSuffix}'
  location: resourceGroup().location
}

resource diskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-disk-csi-driver-${randomSuffix}'
  location: resourceGroup().location
}

resource fileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-file-csi-driver-${randomSuffix}'
  location: resourceGroup().location
}

resource imageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-image-registry-${randomSuffix}'
  location: resourceGroup().location
}

resource cloudNetworkConfigMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-cp-cloud-network-config-${randomSuffix}'
  location: resourceGroup().location
}

// Data plane identities

resource dpDiskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-disk-csi-driver-${randomSuffix}'
  location: resourceGroup().location
}

resource dpFileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-file-csi-driver-${randomSuffix}'
  location: resourceGroup().location
}

resource dpImageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-dp-image-registry-${randomSuffix}'
  location: resourceGroup().location
}

// Service managed identity

resource serviceManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${clusterName}-service-managed-identity-${randomSuffix}'
  location: resourceGroup().location
}

// Outputs

output userAssignedIdentitiesValue object = {
  controlPlaneOperators: {
    'cluster-api-azure': clusterApiAzureMi.id
    'control-plane': controlPlaneMi.id
    'cloud-controller-manager': cloudControllerManagerMi.id
    'ingress': ingressMi.id
    'disk-csi-driver': diskCsiDriverMi.id
    'file-csi-driver': fileCsiDriverMi.id
    'image-registry': imageRegistryMi.id
    'cloud-network-config': cloudNetworkConfigMi.id
  }
  dataPlaneOperators: {
    'disk-csi-driver': dpDiskCsiDriverMi.id
    'file-csi-driver': dpFileCsiDriverMi.id
    'image-registry': dpImageRegistryMi.id
  }
  serviceManagedIdentity: serviceManagedIdentity.id
}

output identityValue object = {
  type: 'UserAssigned'
  userAssignedIdentities: {
    '${serviceManagedIdentity.id}': {}
    '${clusterApiAzureMi.id}': {}
    '${controlPlaneMi.id}': {}
    '${cloudControllerManagerMi.id}': {}
    '${ingressMi.id}': {}
    '${diskCsiDriverMi.id}': {}
    '${fileCsiDriverMi.id}': {}
    '${imageRegistryMi.id}': {}
    '${cloudNetworkConfigMi.id}': {}
  }
}
