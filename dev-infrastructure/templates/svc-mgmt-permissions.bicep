@description('The name of the CX KeyVault')
param cxKeyVaultName string

@description('The name of the MSI KeyVault')
param msiKeyVaultName string

@description('MSI credentials refresher MI resource ID, used to grant KeyVault access')
param msiRefresherMIResourceId string

@description('CS MI resource ID, used to grant KeyVault access')
param clusterServiceMIResourceId string

@description('Admin API MI resource ID, used to grant resource group introspection access')
param adminApiMIResourceId string

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

//
//   A D M I N   A P I   R E S O U R C E   G R O U P   I N T R O S P E C T I O N   A C C E S S
//

// Reader role
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

var adminApiMIRef = res.msiRefFromId(adminApiMIResourceId)
resource adminApiMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(adminApiMIRef.resourceGroup.subscriptionId, adminApiMIRef.resourceGroup.name)
  name: adminApiMIRef.name
}

resource resourceGroupReaderRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (res.isMsiResourceId(adminApiMIResourceId)) {
  scope: resourceGroup()
  name: guid(resourceGroup().id, adminApiMIResourceId, '00000000-0000-0000-0000-000000000001')
  properties: {
    roleDefinitionId: readerRoleId
    principalId: adminApiMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}
