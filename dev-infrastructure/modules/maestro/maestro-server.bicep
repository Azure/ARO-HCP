/*
This module is responsible for:
 - setting up EventGrid access for the maestro server

Execution scope: the resourcegroup of the AKS cluster where the maestro server
will be deployed.

TODO:
- once Key Vault and EventGrid have network access restrictions enabled,
  this module needs to be enhanced to manage access to both (e.g. privatelink)
*/

param maestroServerManagedIdentityPrincipalId string

param maestroInfraResourceGroup string
param maestroEventGridNamespaceName string
param maestroKeyVaultName string
param maestroKeyVaultOfficerManagedIdentityName string
param maestroKeyVaultCertificateDomain string

param location string

module evengGridAccess './maestro-eventgrid-access.bicep' = {
  name: '${deployment().name}-event-grid-access'
  scope: resourceGroup(maestroInfraResourceGroup)
  params: {
    eventGridNamespaceName: maestroEventGridNamespaceName
    keyVaultName: maestroKeyVaultName
    kvCertOfficerManagedIdentityName: maestroKeyVaultOfficerManagedIdentityName
    certDomain: maestroKeyVaultCertificateDomain
    clientName: 'maestro-server'
    clientRole: 'server'
    certificateAccessManagedIdentityPrincipalId: maestroServerManagedIdentityPrincipalId
    location: location
  }
}
