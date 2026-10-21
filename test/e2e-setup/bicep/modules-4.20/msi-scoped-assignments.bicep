targetScope = 'resourceGroup'

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
@description('Identities to assign')
param identities ManagedIdentities

@description('RBAC scope to use for role assignments: resourceGroup or resource')
@allowed([
  'resource'
  'resourceGroup'
])
param rbacScope string = 'resourceGroup'

resource clusterApiAzureMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.clusterApiAzureMiName
}

resource controlPlaneMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.controlPlaneMiName
}

resource cloudControllerManagerMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.cloudControllerManagerMiName
}

resource ingressMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.ingressMiName
}

resource diskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.diskCsiDriverMiName
}

resource fileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.fileCsiDriverMiName
}

resource imageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.imageRegistryMiName
}

resource cloudNetworkConfigMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.cloudNetworkConfigMiName
}

resource kmsMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.kmsMiName
}

resource dpDiskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.dpDiskCsiDriverMiName
}

resource dpFileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.dpFileCsiDriverMiName
}

resource dpImageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.dpImageRegistryMiName
}

resource serviceManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identities.serviceManagedIdentityName
}

//
// R O L E   D E F I N I T I O N S
//

// Reader: Grants permission to read Azure resources
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

// Azure Red Hat OpenShift Federated Credential: Create, update and delete federated credentials on user assigned 
// managed identities in order to build a trust relationship between the managed identity, OpenID Connect (OIDC), and the service account.
// https://www.azadvertizer.net/azrolesadvertizer/ef318e2a-8334-4a05-9e4a-295a196c6a6e.html
var federatedCredentialsRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'ef318e2a-8334-4a05-9e4a-295a196c6a6e'
)

//
// R E A D E R   R O L E   (service MSI -> control-plane MSIs)
//
resource serviceMiReaderOnIdentitiesResourceGroup 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resourceGroup') {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId)
  scope: resourceGroup()
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceMiReaderOnClusterApi 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(clusterApiAzureMi.id, serviceManagedIdentity.id, readerRoleId)
  scope: clusterApiAzureMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceMiReaderOnControlPlane 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(controlPlaneMi.id, serviceManagedIdentity.id, readerRoleId)
  scope: controlPlaneMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceMiReaderOnCloudControllerManager 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(cloudControllerManagerMi.id, serviceManagedIdentity.id, readerRoleId)
  scope: cloudControllerManagerMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceMiReaderOnIngress 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(ingressMi.id, serviceManagedIdentity.id, readerRoleId)
  scope: ingressMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceMiReaderOnDiskCsiDriver 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(diskCsiDriverMi.id, serviceManagedIdentity.id, readerRoleId)
  scope: diskCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceMiReaderOnFileCsiDriver 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(fileCsiDriverMi.id, serviceManagedIdentity.id, readerRoleId)
  scope: fileCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceMiReaderOnImageRegistry 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(imageRegistryMi.id, serviceManagedIdentity.id, readerRoleId)
  scope: imageRegistryMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceMiReaderOnCloudNetworkConfig 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(cloudNetworkConfigMi.id, serviceManagedIdentity.id, readerRoleId)
  scope: cloudNetworkConfigMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceMiReaderOnKms 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(kmsMi.id, serviceManagedIdentity.id, readerRoleId)
  scope: kmsMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// F E D E R A T E D   C R E D E N T I A L   R O L E   (service MSI -> dataplane MSIs)
//
resource serviceMiFederatedCredentialsOnIdentitiesResourceGroup 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resourceGroup') {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, federatedCredentialsRoleId)
  scope: resourceGroup()
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: federatedCredentialsRoleId
  }
}

resource dpDiskCsiDriverMiFederatedCredentialsRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(dpDiskCsiDriverMi.id, serviceManagedIdentity.id, federatedCredentialsRoleId)
  scope: dpDiskCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: federatedCredentialsRoleId
  }
}

resource dpFileCsiDriverMiFederatedCredentialsRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(dpFileCsiDriverMi.id, serviceManagedIdentity.id, federatedCredentialsRoleId)
  scope: dpFileCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: federatedCredentialsRoleId
  }
}

resource dpImageRegistryMiFederatedCredentialsRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (rbacScope == 'resource') {
  name: guid(dpImageRegistryMi.id, serviceManagedIdentity.id, federatedCredentialsRoleId)
  scope: dpImageRegistryMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: federatedCredentialsRoleId
  }
}