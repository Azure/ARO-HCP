@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The name of the resourcegroup for the service keyvault')
param serviceKeyVaultResourceGroup string = resourceGroup().name

@description('The location of the resourcegroup for the service keyvault')
param serviceKeyVaultLocation string = resourceGroup().location

@description('Soft delete setting for service keyvault')
param serviceKeyVaultSoftDelete bool = true

@description('If true, make the service keyvault private and only accessible by the svc cluster via private link.')
param serviceKeyVaultPrivate bool = true

@description('MSI that will be used to run the deploymentScript')
param aroDevopsMsiId string

@description('Frontend Certificate Name')
param certName string

@description('This is a regional DNS zone')
param regionalDNSZoneName string

//
//   K E Y V A U L T S
//

module serviceKeyVault '../modules/keyvault/keyvault.bicep' = {
  name: '${deployment().name}-svcs-kv'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    location: serviceKeyVaultLocation
    keyVaultName: serviceKeyVaultName
    private: serviceKeyVaultPrivate
    enableSoftDelete: serviceKeyVaultSoftDelete
    purpose: 'service'
  }
}
output svcKeyVaultName string = serviceKeyVault.outputs.kvName

//
//   C E R T I F I C A T E   C R E A T I O N
//

var clientAuthenticationName = 'frontend.${regionalDNSZoneName}'

module clientCertificate '../modules/keyvault/key-vault-cert.bicep' = {
  name: 'frontend-cert'
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVault.outputs.kvName
    subjectName: 'CN=frontend'
    certName: certName
    keyVaultManagedIdentityId: aroDevopsMsiId
    dnsNames: [
      clientAuthenticationName
    ]
    issuerName: 'Self' // TODO: Change to OneCertV2-PublicCA when we get the issuer approved.
  }
}

//
//  C E R T I F I C A T E   A C C E S S   P E R M I S S I O N
//

var keyVaultSecretUserRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4633458b-17de-408a-b874-0445c86b69e6'
)

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: serviceKeyVaultName
}

resource frontendMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'frontend'
  location: resourceGroup().location
}

// grant permissions on the secret that contains the certificate

resource secret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' existing = {
  parent: kv
  name: certName
}

resource secretAccessPermission 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: secret
  name: guid('frontend', keyVaultSecretUserRoleId, kv.id, certName)
  properties: {
    roleDefinitionId: keyVaultSecretUserRoleId
    principalId: frontendMSI.properties.principalId
    principalType: 'ServicePrincipal'
  }
}
