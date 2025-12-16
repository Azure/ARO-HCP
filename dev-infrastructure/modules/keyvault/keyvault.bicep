@description('Location of the keyvault.')
param location string

@description('Name of the key vault.')
param keyVaultName string

@description('Toggle to enable soft delete.')
param enableSoftDelete bool

@description('Toggle to make the keyvault private.')
param private bool

@description('Tag key for the keyvault.')
param tagKey string

@description('Tag value for the keyvault.')
param tagValue string

@description('Principal ID for KV certificate officer')
param kvCertOfficerPrincipalId string = ''

@description('Principal ID for EV2 certificate access, i.e. geneva log/action access')
param kvCertAccessPrincipalId string = ''

@description('Roles used for EV2 KeyVault access, i.e. geneva log/action access')
param kvCertAccessRoleId string = ''

resource keyVault 'Microsoft.KeyVault/vaults@2024-04-01-preview' = {
  location: location
  name: keyVaultName
  tags: {
    resourceGroup: resourceGroup().name
    '${tagKey}': tagValue
  }
  properties: {
    enableRbacAuthorization: true
    enabledForDeployment: false
    enabledForDiskEncryption: false
    enabledForTemplateDeployment: false
    enableSoftDelete: enableSoftDelete
    publicNetworkAccess: private ? 'Disabled' : 'Enabled'
    sku: {
      name: 'standard'
      family: 'A'
    }
    tenantId: subscription().tenantId
  }
}

//
//   E V 2    K V    A C C E S S
//

module kvCertOfficer 'keyvault-secret-access.bicep' = if (kvCertOfficerPrincipalId != '') {
  name: guid(kvCertOfficerPrincipalId, keyVaultName, 'cert-officer')
  params: {
    keyVaultName: keyVaultName
    roleName: 'Key Vault Certificates Officer'
    managedIdentityPrincipalIds: [kvCertOfficerPrincipalId]
  }
}

module kvSecretsOfficer 'keyvault-secret-access.bicep' = if (kvCertOfficerPrincipalId != '') {
  name: guid(kvCertOfficerPrincipalId, keyVaultName, 'secrets-officer')
  params: {
    keyVaultName: keyVaultName
    roleName: 'Key Vault Secrets Officer'
    managedIdentityPrincipalIds: [kvCertOfficerPrincipalId]
  }
}

module ev2CertAccess 'keyvault-secret-access.bicep' = if (kvCertAccessPrincipalId != '' && kvCertAccessRoleId != '') {
  name: guid(kvCertOfficerPrincipalId, keyVaultName, 'certificate-access')
  params: {
    keyVaultName: keyVaultName
    roleName: 'Azure Service Deploy Release Management Key Vault Secrets User'
    managedIdentityPrincipalIds: [kvCertAccessPrincipalId]
    kvCertAccessRoleId: kvCertAccessRoleId
  }
}

output kvId string = keyVault.id

output kvName string = keyVault.name

output kvUrl string = keyVault.properties.vaultUri
