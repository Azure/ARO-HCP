@description('Azure Region Location')
param location string = resourceGroup().location

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('Defines if the CX KeyVault is private')
param cxKeyVaultPrivate bool

@description('Defines if the CX KeyVault has soft delete enabled')
param cxKeyVaultSoftDelete bool

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('Defines if the MSI KeyVault is private')
param msiKeyVaultPrivate bool

@description('Defines if the MSI KeyVault has soft delete enabled')
param msiKeyVaultSoftDelete bool

@description('The name of the MGMT KeyVault')
param mgmtKeyVaultName string

@description('Defines if the MGMT KeyVault is private')
param mgmtKeyVaultPrivate bool

@description('Defines if the MGMT KeyVault has soft delete enabled')
param mgmtKeyVaultSoftDelete bool

@description('Cluster user assigned identity resource id, used to grant KeyVault access')
param clusterServiceMIResourceId string

@description('KV certificate officer principal ID')
param kvCertOfficerPrincipalId string

@description('MSI that will be used during pipeline runs')
param aroDevopsMsiId string

// Log Analytics Workspace ID will be passed from region pipeline if enabled in config
param logAnalyticsWorkspaceId string = ''

resource resourcegroupTags 'Microsoft.Resources/tags@2024-03-01' = {
  name: 'default'
  scope: resourceGroup()
  properties: {
    tags: {
      persist: toLower(string(persist))
    }
  }
}

// Reader role
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

// service deployments running as the aroDevopsMsi need to lookup metadata about all kinds
// of resources, e.g. AKS metadata, database metadata, MI metadata, etc.
resource aroDevopsMSIReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, aroDevopsMsiId, readerRoleId)
  properties: {
    principalId: reference(aroDevopsMsiId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
//   K E Y V A U L T S
//

module cxKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: '${deployment().name}-cx-kv'
  params: {
    location: location
    keyVaultName: cxKeyVaultName
    private: cxKeyVaultPrivate
    enableSoftDelete: cxKeyVaultSoftDelete
    purpose: 'cx'
    logAnalyticsWorkspaceId: logAnalyticsWorkspaceId
  }
}

module cxKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(cxKeyVaultName, kvCertOfficerPrincipalId, role)
    params: {
      keyVaultName: cxKeyVaultName
      roleName: role
      managedIdentityPrincipalId: kvCertOfficerPrincipalId
    }
    dependsOn: [
      cxKeyVault
    ]
  }
]

output cxKeyVaultUrl string = cxKeyVault.outputs.kvUrl

module msiKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: '${deployment().name}-msi-kv'
  params: {
    location: location
    keyVaultName: msiKeyVaultName
    private: msiKeyVaultPrivate
    enableSoftDelete: msiKeyVaultSoftDelete
    purpose: 'msi'
    logAnalyticsWorkspaceId: logAnalyticsWorkspaceId
  }
}

output msiKeyVaultUrl string = msiKeyVault.outputs.kvUrl

module mgmtKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: '${deployment().name}-mgmt-kv'
  params: {
    location: location
    keyVaultName: mgmtKeyVaultName
    private: mgmtKeyVaultPrivate
    enableSoftDelete: mgmtKeyVaultSoftDelete
    purpose: 'mgmt'
    logAnalyticsWorkspaceId: logAnalyticsWorkspaceId
  }
}

module mgmtKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(mgmtKeyVaultName, kvCertOfficerPrincipalId, role)
    params: {
      keyVaultName: mgmtKeyVaultName
      roleName: role
      managedIdentityPrincipalId: kvCertOfficerPrincipalId
    }
    dependsOn: [
      mgmtKeyVault
    ]
  }
]

output mgmtKeyVaultUrl string = mgmtKeyVault.outputs.kvUrl

//
//   C L U S T E R   S E R V I C E   K V   A C C E S S
//

import * as res from '../modules/resource.bicep'

module csKeyVaultAccess '../modules/cluster-service-mc-kv-access.bicep' = if (res.isMsiResourceId(clusterServiceMIResourceId)) {
  name: 'cs-msi-kv-access'
  params: {
    clusterServiceMIResourceId: clusterServiceMIResourceId
    cxKeyVaultName: cxKeyVaultName
    msiKeyVaultName: msiKeyVaultName
  }
  dependsOn: [
    cxKeyVault
    msiKeyVault
  ]
}
