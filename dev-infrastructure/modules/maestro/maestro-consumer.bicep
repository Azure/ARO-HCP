/*
This module is responsible for setting up EventGrid access for the maestro consumer by
- create a client certificate
- register the client in the EventGrid namespace

Execution scope: the resourcegroup of the MC where the agent is deployed.
*/

param maestroAgentManagedIdentityPrincipalId string
@minLength(1)
param maestroConsumerName string
param maestroEventGridNamespaceId string

param certKeyVaultName string
param keyVaultOfficerManagedIdentityName string
param maestroCertificateDomain string
param maestroCertificateIssuer string

module eventGridClientCert '../keyvault/key-vault-cert-with-access.bicep' = {
  name: 'maestro-eg-crt-${uniqueString(maestroConsumerName)}'
  params: {
    keyVaultName: certKeyVaultName
    kvCertOfficerManagedIdentityResourceId: keyVaultOfficerManagedIdentityName
    certDomain: maestroCertificateDomain
    certificateIssuer: maestroCertificateIssuer
    hostName: maestroConsumerName
    keyVaultCertificateName: maestroConsumerName
    certificateAccessManagedIdentityPrincipalId: maestroAgentManagedIdentityPrincipalId
  }
}

import * as res from '../resource.bicep'

var eventGridNamespaceRef = res.eventgridNamespaceRefFromId(maestroEventGridNamespaceId)

module evengGridAccess 'maestro-eventgrid-access.bicep' = {
  name: 'eg-access-${uniqueString(maestroConsumerName)}'
  scope: resourceGroup(eventGridNamespaceRef.resourceGroup.subscriptionId, eventGridNamespaceRef.resourceGroup.name)
  params: {
    eventGridNamespaceName: eventGridNamespaceRef.name
    clientName: maestroConsumerName
    clientRole: 'consumer'
    certificateThumbprint: eventGridClientCert.outputs.certificateThumbprint
    certificateSAN: eventGridClientCert.outputs.certificateSAN
    certificateIssuer: maestroCertificateIssuer
  }
}
