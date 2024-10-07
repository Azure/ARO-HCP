@description('Azure Region Location')
param location string = resourceGroup().location

@description('The name of the key vault')
param keyVaultName string

@description('Azure Region Location')
param kvCertOfficerManagedIdentityName string

//
// M A N A G E D   I D E N T I T Y   C R E A T I O N
//

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}

resource kvCertOfficerManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: kvCertOfficerManagedIdentityName
  location: location
}

var keyVaultCertificateOfficerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  'a4417e6f-fecd-4de8-b567-7b0420556985'
)

resource kvManagedIdentityRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: kv
  name: guid(kvCertOfficerManagedIdentity.id, keyVaultCertificateOfficerRoleId, kv.id)
  properties: {
    roleDefinitionId: keyVaultCertificateOfficerRoleId
    principalId: kvCertOfficerManagedIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

output kvCertOfficerManagedIdentityId string = kvCertOfficerManagedIdentity.id
