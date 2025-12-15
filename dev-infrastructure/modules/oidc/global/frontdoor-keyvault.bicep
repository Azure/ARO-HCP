@description('Specifies the name of the key vault.')
param keyVaultName string

@description('The tag key for the Key Vault')
param keyVaultTagKey string

@description('The tag value for the Key Vault')
param keyVaultTagValue string

@description('The Frontdoor Principal ID that is granted KV Certificates Officer permissions')
param frontDoorPrincipalId string

@description('Key vault admin service principal object ID - Used to create a Key Vault access policy for Ev2 extensions')
param keyVaultAdminPrincipalId string

module keyVault '../../keyvault/keyvault.bicep' = {
  name: keyVaultName
  params: {
    keyVaultName: keyVaultName
    location: resourceGroup().location
    enableSoftDelete: false
    private: false
    tagKey: keyVaultTagKey
    tagValue: keyVaultTagValue
  }
}

module keyVaultCertificateOfficer '../../keyvault/keyvault-secret-access.bicep' = {
  name: guid(keyVaultName, keyVaultAdminPrincipalId, 'Key Vault Certificates Officer')
  params: {
    keyVaultName: keyVaultName
    roleName: 'Key Vault Certificates Officer'
    managedIdentityPrincipalIds: [keyVaultAdminPrincipalId]
  }
  dependsOn: [
    keyVault
  ]
}

module keyVaultSecretsUser '../../keyvault/keyvault-secret-access.bicep' = {
  name: guid(keyVaultName, frontDoorPrincipalId, 'Key Vault Secrets User')
  params: {
    keyVaultName: keyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalIds: [frontDoorPrincipalId]
  }
  dependsOn: [
    keyVault
  ]
}
