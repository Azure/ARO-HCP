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

@description('MSI identities in the pool')
param identities ManagedIdentities

// Control-plane operator identities
resource clusterApiAzureMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.clusterApiAzureMiName
  location: resourceGroup().location
}

resource controlPlaneMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.controlPlaneMiName
  location: resourceGroup().location
}

resource cloudControllerManagerMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.cloudControllerManagerMiName
  location: resourceGroup().location
}

resource ingressMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.ingressMiName
  location: resourceGroup().location
}

resource diskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.diskCsiDriverMiName
  location: resourceGroup().location
}

resource fileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.fileCsiDriverMiName
  location: resourceGroup().location
}

resource imageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.imageRegistryMiName
  location: resourceGroup().location
}

resource cloudNetworkConfigMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.cloudNetworkConfigMiName
  location: resourceGroup().location
}

resource kmsMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.kmsMiName
  location: resourceGroup().location
}

// Dataplane operator identities
resource dpDiskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.dpDiskCsiDriverMiName
  location: resourceGroup().location
}

resource dpFileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.dpFileCsiDriverMiName
  location: resourceGroup().location
}

resource dpImageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.dpImageRegistryMiName
  location: resourceGroup().location
}

// Service managed identity
resource serviceManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identities.serviceManagedIdentityName
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


