@description('Location of the keyvault.')
param location string

@description('Name of the key vault.')
param genevaKeyVaultName string
@description('Should the geneva actions keyvault be private')
param genevaKeyVaultPrivate bool
@description('Should the geneva actions keyvault have soft delete enabled')
param genevaKeyVaultSoftDelete bool
@description('Tag key for the geneva actions keyvault')
param genevaKeyVaultTagKey string
@description('Tag value for the geneva actions keyvault')
param genevaKeyVaultTagValue string
@description('Name of geneva action extensions')
param allowedAcisExtensions string
@description('App ID for Geneva Actions')
param genevaActionsPrincipalId string
@description('Principal ID for KV certificate officer')
param kvCertOfficerPrincipalId string
@description('Principal ID for EV2 certificate access, i.e. geneva log/action access')
param kvCertAccessPrincipalId string
@description('Roles used for EV2 KeyVault access, i.e. geneva log/action access')
param kvCertAccessRoleId string

@description('App name for the Geneva Action Application we will use for downstream automation towards the Admin API')
param genevaActionApplicationName string
@description('SNI for Geneva Action Application')
param genevaActionApplicationCertificateSubjectName string
@description('The owner of the geneva actions application')
param genevaActionApplicationOwnerId string


//   G E N E V A    K V

module genevaKv '../modules/keyvault/keyvault.bicep' = {
  name: 'geneva-kv'
  params: {
    location: location
    keyVaultName: genevaKeyVaultName
    private: genevaKeyVaultPrivate
    enableSoftDelete: genevaKeyVaultSoftDelete
    tagKey: genevaKeyVaultTagKey
    tagValue: genevaKeyVaultTagValue
    kvCertOfficerPrincipalId: kvCertOfficerPrincipalId
    kvCertAccessPrincipalId: kvCertAccessPrincipalId
    kvCertAccessRoleId: kvCertAccessRoleId
  }
}

output genevaKeyVaultUrl string = genevaKv.outputs.kvUrl

module genevaKvSecretsUserAccessToGenevaApp '../modules/keyvault/keyvault-secret-access.bicep' = if (genevaActionsPrincipalId != '') {
  name: guid(genevaKeyVaultName, 'KeyVaultAccess', 'Key Vault Secrets User', genevaActionsPrincipalId)
  params: {
    keyVaultName: genevaKv.outputs.kvName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: genevaActionsPrincipalId
  }
}

module genevaKvReaderAccessToGenevaApp '../modules/keyvault/keyvault-secret-access.bicep' = if (genevaActionsPrincipalId != '') {
  name: guid(genevaKeyVaultName, 'KeyVaultAccess', 'Key Vault Reader', genevaActionsPrincipalId)
  params: {
    keyVaultName: genevaKv.outputs.kvName
    roleName: 'Key Vault Reader'
    managedIdentityPrincipalId: genevaActionsPrincipalId
  }
}

resource allowedExtensionsSecret 'Microsoft.KeyVault/vaults/secrets@2021-04-01-preview' = {
  name: '${genevaKeyVaultName}/AllowedAcisExtensions'
  properties: {
    value: allowedAcisExtensions
  }
  dependsOn: [
    genevaKv
  ]
}

//   A P P   R E G I S T R A T I O N

extension microsoftGraphBeta

resource genevaApp 'Microsoft.Graph/applications@beta' = {
  displayName: genevaActionApplicationName
  isFallbackPublicClient: true
  signInAudience: 'AzureADMyOrg' // Single tenant applicaion
  uniqueName: genevaActionApplicationName
  info: {}
  requiredResourceAccess: []
  publicClient: {
    redirectUris: []
  }
  web: {
    redirectUris: []
    logoutUrl: null
    implicitGrantSettings: {
      enableIdTokenIssuance: true
      enableAccessTokenIssuance: false
    }
  }
  spa: {
    redirectUris: []
  }
  serviceManagementReference: 'b8e9ef87-cd63-4085-ab14-1c637806568c'
  trustedSubjectNameAndIssuers: [
    {
      authorityId: '00000000-0000-0000-0000-000000000001'
      subjectName: genevaActionApplicationCertificateSubjectName
    }
  ]
  owners: {
    relationships: [
      genevaActionApplicationOwnerId
    ]
  }
}

// resource sp 'Microsoft.Graph/servicePrincipals@v1.0' = {
//   appId: app.appId
// }
