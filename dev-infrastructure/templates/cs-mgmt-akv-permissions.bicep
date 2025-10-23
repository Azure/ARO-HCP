@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('MSI credentials refresher MI resource ID, used to grant KeyVault access')
param msiRefresherMIResourceId string

@description('CS MI resource ID, used to grant KeyVault access')
param clusterServiceMIResourceId string

resource cxKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: cxKeyVaultName
}

resource msiKeyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: msiKeyVaultName
}

//
//   C L U S T E R   S E R V I C E   K V   A C C E S S
//

import * as res from '../modules/resource.bicep'

module csKeyVaultAccess '../modules/mgmt-kv-access.bicep' = if (res.isMsiResourceId(clusterServiceMIResourceId)) {
  name: 'cs-msi-kv-access'
  params: {
    managedIdentityResourceId: clusterServiceMIResourceId
    cxKeyVaultName: cxKeyVault.name
    msiKeyVaultName: msiKeyVault.name
  }
}

//
//   M S I   C R E D E N T I A L S   R E F R E S H E R   K V   A C C E S S
//

module msiRefresherKeyVaultAccess '../modules/mgmt-kv-access.bicep' = if (res.isMsiResourceId(msiRefresherMIResourceId)) {
  name: 'msi-refresher-msi-kv-access'
  params: {
    managedIdentityResourceId: msiRefresherMIResourceId
    cxKeyVaultName: ''
    msiKeyVaultName: msiKeyVault.name
  }
}
