@description('Managed identity resource id that gets access to the specified KeyVaults.')
param managedIdentityResourceId string

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

//
//   C L U S T E R   S E R V I C E   K V   A C C E S S
//

import * as res from 'resource.bicep'
var mIRef = res.msiRefFromId(managedIdentityResourceId)

resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(mIRef.resourceGroup.subscriptionId, mIRef.resourceGroup.name)
  name: mIRef.name
}

module cxClusterServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: if (cxKeyVaultName != '') {
    name: guid(cxKeyVaultName, managedIdentityResourceId, role)
    params: {
      keyVaultName: cxKeyVaultName
      roleName: role
      managedIdentityPrincipalId: managedIdentity.properties.principalId
    }
  }
]

module msiClusterServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = [
  for role in [
    'Key Vault Secrets Officer'
    'Key Vault Certificate User'
    'Key Vault Certificates Officer'
  ]: if (msiKeyVaultName != '') {
    name: guid(msiKeyVaultName, managedIdentityResourceId, role)
    params: {
      keyVaultName: msiKeyVaultName
      roleName: role
      managedIdentityPrincipalId: managedIdentity.properties.principalId
    }
  }
]
