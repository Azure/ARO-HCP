@description('The service managed identity resource ID')
param serviceManagedIdentityId string

@description('The cluster API Azure managed identity resource ID')
param clusterApiAzureMiId string

@description('The control plane managed identity resource ID')
param controlPlaneMiId string

@description('The cloud controller manager managed identity resource ID')
param cloudControllerManagerMiId string

@description('The ingress managed identity resource ID')
param ingressMiId string

@description('The disk CSI driver managed identity resource ID')
param diskCsiDriverMiId string

@description('The file CSI driver managed identity resource ID')
param fileCsiDriverMiId string

@description('The image registry managed identity resource ID')
param imageRegistryMiId string

@description('The cloud network config managed identity resource ID')
param cloudNetworkConfigMiId string

@description('The KMS managed identity resource ID')
param kmsMiId string

//
// E X I S T I N G   R E S O U R C E S
//

resource serviceManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(serviceManagedIdentityId, '/'))
}

resource clusterApiAzureMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(clusterApiAzureMiId, '/'))
}

resource controlPlaneMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(controlPlaneMiId, '/'))
}

resource cloudControllerManagerMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(cloudControllerManagerMiId, '/'))
}

resource ingressMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(ingressMiId, '/'))
}

resource diskCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(diskCsiDriverMiId, '/'))
}

resource fileCsiDriverMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(fileCsiDriverMiId, '/'))
}

resource imageRegistryMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(imageRegistryMiId, '/'))
}

resource cloudNetworkConfigMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(cloudNetworkConfigMiId, '/'))
}

resource kmsMi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: last(split(kmsMiId, '/'))
}

//
// R O L E   D E F I N I T I O N S
//

var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

//
// R E A D E R   R O L E   A S S I G N M E N T S
//

resource serviceManagedIdentityReaderOnClusterApiAzureMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, clusterApiAzureMi.id)
  scope: clusterApiAzureMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceManagedIdentityReaderOnControlPlaneMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, controlPlaneMi.id)
  scope: controlPlaneMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceManagedIdentityReaderOnCloudControllerManagerMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, cloudControllerManagerMi.id)
  scope: cloudControllerManagerMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceManagedIdentityReaderOnIngressMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, ingressMi.id)
  scope: ingressMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceManagedIdentityReaderOnDiskCsiDriverMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, diskCsiDriverMi.id)
  scope: diskCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceManagedIdentityReaderOnFileCsiDriverMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, fileCsiDriverMi.id)
  scope: fileCsiDriverMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceManagedIdentityReaderOnImageRegistryMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, imageRegistryMi.id)
  scope: imageRegistryMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceManagedIdentityReaderOnCloudNetworkMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, cloudNetworkConfigMi.id)
  scope: cloudNetworkConfigMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

resource serviceManagedIdentityReaderOnKMSAzureMi 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, serviceManagedIdentity.id, readerRoleId, kmsMi.id)
  scope: kmsMi
  properties: {
    principalId: serviceManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}
