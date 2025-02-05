/*
Creates a certificate in Key Vault signed by the specified issuer.
For dev environments `Self` is used as issuer, for higher environments
OneCertV2 Private will be used.

The specified managed identity `certificateAccessManagedIdentityPrincipalId`
is granted access to the certificate in Key Vault. This will be leveraged
with CSI secret store to access the certificate from the maestro pods.

Execution scope: the resourcegroup of the Key Vault where the certificate will be stored
*/

@description('The Key Vault where the certificate for Event Grid access will be stored')
param keyVaultName string

@description('The managed identity that will be used to manage the certificate in Key Vault')
param kvCertOfficerManagedIdentityResourceId string

@description('The base domain name to be used for the certificates DNS name.')
param certDomain string

@description('The name of the client that will be created in the EventGrid Namespace')
param clientName string

@description('The name of the certificate in Key Vault.')
param keyVaultCertificateName string

@description('The issuer of the certificate.')
param certificateIssuer string

@description('Grant this managed identity access to the certificate in Key Vault.')
param certificateAccessManagedIdentityPrincipalId string

//
//   C E R T I F I C A T E   C R E A T I O N
//

var clientAuthenticationName = '${clientName}.${certDomain}'

module clientCertificate '../keyvault/key-vault-cert.bicep' = {
  name: '${clientName}-client-cert'
  params: {
    keyVaultName: keyVaultName
    subjectName: 'CN=${clientName}'
    certName: keyVaultCertificateName
    keyVaultManagedIdentityId: kvCertOfficerManagedIdentityResourceId
    dnsNames: [
      clientAuthenticationName
    ]
    issuerName: certificateIssuer
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
  name: keyVaultName
}

// grant permissions on the secret that contains the certificate

resource secret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' existing = {
  parent: kv
  name: keyVaultCertificateName
}

resource secretAccessPermission 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: secret
  name: guid(certificateAccessManagedIdentityPrincipalId, keyVaultSecretUserRoleId, kv.id, keyVaultCertificateName)
  properties: {
    roleDefinitionId: keyVaultSecretUserRoleId
    principalId: certificateAccessManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

output certificateThumbprint string = clientCertificate.outputs.Thumbprint
output certificateSAN string = clientAuthenticationName
