/*
This module is responsible for setting up EventGrid access for the maestro consumer by
- granting access to the client certificate in Key Vault
- registering the client in the EventGrid namespace

Execution scope: the resourcegroup of the MC where the agent is deployed.
*/

param maestroAgentManagedIdentityPrincipalId string
@minLength(1)
param maestroConsumerName string
param maestroEventGridNamespaceId string

param certKeyVaultName string

@description('The subject alternative name of the certificate')
param certificateSAN string

@description('The issuer of the certificate.')
param certificateIssuer string

//
//   C E R T I F I C A T E   A C C E S S
//

module certSecretAccess '../keyvault/key-vault-secret-access.bicep' = {
  name: 'maestro-cert-access-${uniqueString(maestroConsumerName)}'
  params: {
    keyVaultName: certKeyVaultName
    secretName: maestroConsumerName
    principalId: maestroAgentManagedIdentityPrincipalId
  }
}

//
//   C E R T I F I C A T E   T H U M B P R I N T
//

resource kv 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: certKeyVaultName
}

resource certSecret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' existing = {
  parent: kv
  name: maestroConsumerName
}

//
//   E V E N T G R I D   A C C E S S
//

import * as res from '../resource.bicep'

var eventGridNamespaceRef = res.eventgridNamespaceRefFromId(maestroEventGridNamespaceId)

module evengGridAccess 'maestro-eventgrid-access.bicep' = {
  name: 'eg-access-${uniqueString(maestroConsumerName)}'
  scope: resourceGroup(eventGridNamespaceRef.resourceGroup.subscriptionId, eventGridNamespaceRef.resourceGroup.name)
  params: {
    eventGridNamespaceName: eventGridNamespaceRef.name
    clientName: maestroConsumerName
    clientRole: 'consumer'
    certificateSAN: certificateSAN
    certificateIssuer: certificateIssuer
    certificateThumbprint: certSecret.?tags.?thumbprint ?? ''
  }
}
