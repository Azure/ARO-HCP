@description('Managed identity resource ids that gets access to the specified KeyVaults.')
param managedIdentityResourceIds array

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

//
//   C L U S T E R   S E R V I C E   K V   A C C E S S
//

import * as res from 'resource.bicep'
var miRefs = [for miId in managedIdentityResourceIds: res.msiRefFromId(miId)]

resource managedIdentities 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = [
  for miRef in miRefs: {
    scope: resourceGroup(miRef.resourceGroup.subscriptionId, miRef.resourceGroup.name)
    name: miRef.name
  }
]

module cxKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: if (cxKeyVaultName != '') {
    name: 'cx-access-${uniqueString(role, join(managedIdentityResourceIds, ','))}'
    params: {
      keyVaultName: cxKeyVaultName
      roleName: role
      managedIdentityPrincipalIds: [for (miRef, i) in miRefs: managedIdentities[i].properties.principalId]
    }
  }
]

module msiKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: if (msiKeyVaultName != '') {
    name: 'msi-access-${uniqueString(role, join(managedIdentityResourceIds, ','))}'
    params: {
      keyVaultName: msiKeyVaultName
      roleName: role
      managedIdentityPrincipalIds: [for (miRef, i) in miRefs: managedIdentities[i].properties.principalId]
    }
  }
]
