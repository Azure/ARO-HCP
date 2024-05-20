/*
This module manages access to EventGrid for a maestro client, which
can be the server or a consumer.

- Creates a certificate in Key Vault signed by the specified issuer.
  For dev environments `Self` is used as issuer, for higher environments
  OneCertV2 Private will be used.
- An MQTT client is registered within eventgrid. Depending on the certificate
  issuer, the certificate validation schema will be thumbprint based for
  self-signed certificates and DNS based for OneCertV2 Private certificates.
- The MQTT client is placed into the right MQTT client group based on the
  client role. This defines the topic access permissions for the client.
- The specified managed identity `certificateAccessManagedIdentityPrincipalId`
  is granted access to the certificate in Key Vault. This will be leveraged
  with CSI secret store to access the certificate from the maestro pods.

Execution scope: the resourcegroup of the maestro infrastructure
*/

@description('The EventGrid Namespace name where access will be managed')
param eventGridNamespaceName string

@description('The Key Vault name where the certificate for Event Grid access will be stored')
param keyVaultName string

@description('The name of the managed identity that will be used to manage the certificate in Key Vault')
param kvCertOfficerManagedIdentityName string

@description('The base domain name to be used for the certificates DNS name.')
param certDomain string

@description('The name of the client that will be created in the EventGrid Namespace')
param clientName string

@description('The role of the client in the EventGrid Namespace.')
@allowed([
  'server'
  'consumer'
])
param clientRole string

@description('Grant this managed identity access to the certificate in Key Vault.')
param certificateAccessManagedIdentityPrincipalId string

@description('The issuer of the certificate.')
param certificateIssuer string = 'Self'

param location string

var clientAuthenticationName = '${clientName}.${certDomain}'

resource eventGridNamespace 'Microsoft.EventGrid/namespaces@2023-12-15-preview' existing = {
  name: eventGridNamespaceName
}

resource kvCertOfficerManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: kvCertOfficerManagedIdentityName
}

// certificate for MQTT authentication
module clientCertificate '../key-vault-cert.bicep' = {
  name: '${deployment().name}-client-cert'
  params: {
    location: location
    keyVaultName: keyVaultName
    subjectName: 'CN=${clientName}'
    certName: clientName
    keyVaultManagedIdentityId: kvCertOfficerManagedIdentity.id
    dnsNames: [
      clientAuthenticationName
    ]
    // todo - use Private OnceCertV2 in higher environments
    issuerName: certificateIssuer
  }
}

// D O N ' T   U S E   T H I S   I N   P R O D U C T I O N
// eventgrid MQTT client trusting the certificate by thumbprint if
// Key Vault self-signed certificates are used. trusting self-signed certificates
// as CAs is not supported in EventGrid
resource mqttClient 'Microsoft.EventGrid/namespaces/clients@2023-12-15-preview' = if (certificateIssuer == 'Self') {
  name: clientName
  parent: eventGridNamespace
  properties: {
    authenticationName: clientAuthenticationName
    attributes: {
      role: clientRole
      consumer_name: clientName
    }
    clientCertificateAuthentication: {
      allowedThumbprints: [
        clientCertificate.outputs.Thumbprint
      ]
      validationScheme: 'ThumbprintMatch'
    }
    state: 'Enabled'
  }
}

// TODO - implement issuer CA registration with EventGrid + register the mqtt client with
// the DnsMatchesAuthenticationName authentication validation scheme

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
  name: clientName
}

resource secretAccessPermission 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: secret
  name: guid(certificateAccessManagedIdentityPrincipalId, keyVaultSecretUserRoleId, kv.id)
  properties: {
    roleDefinitionId: keyVaultSecretUserRoleId
    principalId: certificateAccessManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

// output

output KeyVaultCertId string = clientCertificate.outputs.KeyVaultCertId
output KeyVaultCertName string = clientName
output EventGridHostname string = eventGridNamespace.properties.topicSpacesConfiguration.hostname
