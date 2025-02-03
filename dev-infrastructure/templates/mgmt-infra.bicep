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

resource resourcegroupTags 'Microsoft.Resources/tags@2024-03-01' = {
  name: 'default'
  scope: resourceGroup()
  properties: {
    tags: {
      persist: toLower(string(persist))
    }
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
  }
}

output cxKeyVaultUrl string = cxKeyVault.outputs.kvUrl

module msiKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: '${deployment().name}-msi-kv'
  params: {
    location: location
    keyVaultName: msiKeyVaultName
    private: msiKeyVaultPrivate
    enableSoftDelete: msiKeyVaultSoftDelete
    purpose: 'msi'
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
  }
}

output mgmtKeyVaultUrl string = mgmtKeyVault.outputs.kvUrl

//
//   C L U S T E R   S E R V I C E   K V   A C C E S S
//

import * as res from '../modules/resource.bicep'
var clusterServiceMIRef = res.msiRefFromId(clusterServiceMIResourceId)

resource clusterServiceMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(clusterServiceMIRef.resourceGroup.subscriptionId, clusterServiceMIRef.resourceGroup.name)
  name: clusterServiceMIRef.name
}

module cxClusterServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(cxKeyVaultName, clusterServiceMIResourceId, role)
    params: {
      keyVaultName: cxKeyVaultName
      roleName: role
      managedIdentityPrincipalId: clusterServiceMI.properties.principalId
    }
    dependsOn: [
      cxKeyVault
    ]
  }
]

module msiClusterServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: {
    name: guid(msiKeyVaultName, clusterServiceMIResourceId, role)
    params: {
      keyVaultName: msiKeyVaultName
      roleName: role
      managedIdentityPrincipalId: clusterServiceMI.properties.principalId
    }
    dependsOn: [
      msiKeyVault
    ]
  }
]
