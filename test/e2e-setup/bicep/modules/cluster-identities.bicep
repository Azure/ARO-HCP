targetScope = 'resourceGroup'

// Managed Identities layout mirrors the structure used by assignment modules.
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

// Control-plane operator identities
resource clusterApiAzureMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'cluster-api-azure'
  location: resourceGroup().location
}

resource controlPlaneMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'control-plane'
  location: resourceGroup().location
}

resource cloudControllerManagerMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'cloud-controller-manager'
  location: resourceGroup().location
}

resource ingressMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'ingress'
  location: resourceGroup().location
}

resource diskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'disk-csi-driver'
  location: resourceGroup().location
}

resource fileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'file-csi-driver'
  location: resourceGroup().location
}

resource imageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'image-registry'
  location: resourceGroup().location
}

resource cloudNetworkConfigMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'cloud-network-config'
  location: resourceGroup().location
}

resource kmsMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'kms'
  location: resourceGroup().location
}

// Dataplane operator identities
resource dpDiskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'dp-disk-csi-driver'
  location: resourceGroup().location
}

resource dpFileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'dp-file-csi-driver'
  location: resourceGroup().location
}

resource dpImageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'dp-image-registry'
  location: resourceGroup().location
}

// Service managed identity
resource serviceManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'service'
  location: resourceGroup().location
}

@description('Managed identities created in the target resource group')
output msiIdentities ManagedIdentities = {
  clusterApiAzureMiName: clusterApiAzureMi.name
  controlPlaneMiName: controlPlaneMi.name
  cloudControllerManagerMiName: cloudControllerManagerMi.name
  ingressMiName: ingressMi.name
  diskCsiDriverMiName: diskCsiDriverMi.name
  fileCsiDriverMiName: fileCsiDriverMi.name
  imageRegistryMiName: imageRegistryMi.name
  cloudNetworkConfigMiName: cloudNetworkConfigMi.name
  kmsMiName: kmsMi.name
  dpDiskCsiDriverMiName: dpDiskCsiDriverMi.name
  dpFileCsiDriverMiName: dpFileCsiDriverMi.name
  dpImageRegistryMiName: dpImageRegistryMi.name
  serviceManagedIdentityName: serviceManagedIdentity.name
}


