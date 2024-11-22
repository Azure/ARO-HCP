/*
This module is responsible for setting up EventGrid access for the maestro consumer by
- create a client certificate
- register the client in the EventGrid namespace

Execution scope: the resourcegroup of the MC where the agent is deployed.
*/

param maestroAgentManagedIdentityPrincipalId string
@minLength(1)
param maestroConsumerName string
param maestroInfraResourceGroup string
param maestroEventGridNamespaceName string

param certKeyVaultName string
param keyVaultOfficerManagedIdentityName string
param maestroCertificateDomain string

module eventGridClientCert 'maestro-access-cert.bicep' = {
  name: '${deployment().name}-eg-crt-${uniqueString(maestroConsumerName)}'
  params: {
    keyVaultName: certKeyVaultName
    kvCertOfficerManagedIdentityResourceId: keyVaultOfficerManagedIdentityName
    certDomain: maestroCertificateDomain
    clientName: maestroConsumerName
    keyVaultCertificateName: maestroConsumerName
    certificateAccessManagedIdentityPrincipalId: maestroAgentManagedIdentityPrincipalId
  }
}

module evengGridAccess 'maestro-eventgrid-access.bicep' = {
  name: '${deployment().name}-eg-access'
  scope: resourceGroup(maestroInfraResourceGroup)
  params: {
    eventGridNamespaceName: maestroEventGridNamespaceName
    clientName: maestroConsumerName
    clientRole: 'consumer'
    certificateThumbprint: eventGridClientCert.outputs.certificateThumbprint
    certificateSAN: eventGridClientCert.outputs.certificateSAN
  }
}
