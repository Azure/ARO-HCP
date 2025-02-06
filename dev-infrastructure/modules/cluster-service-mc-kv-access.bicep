@description('Cluster user assigned identity resource id, used to grant KeyVault access')
param clusterServiceMIResourceId string

@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

//
//   C L U S T E R   S E R V I C E   K V   A C C E S S
//

import * as res from 'resource.bicep'
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
  }
]
