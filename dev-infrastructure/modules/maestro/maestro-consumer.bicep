param maestroServerManagedIdentityPrincipalId string
@minLength(1)
param maestroConsumerName string
param maestroInfraResourceGroup string
param maestroEventGridNamespaceName string
param maestroKeyVaultName string
param maestroKeyVaultOfficerManagedIdentityName string
param maestroKeyVaultCertificateDomain string

param location string

module evengGridAccess './maestro-eventgrid-access.bicep' = {
  name: 'event-grid-access-${maestroConsumerName}'
  scope: resourceGroup(maestroInfraResourceGroup)
  params: {
    eventGridNamespaceName: maestroEventGridNamespaceName
    keyVaultName: maestroKeyVaultName
    kvCertOfficerManagedIdentityName: maestroKeyVaultOfficerManagedIdentityName
    certDomain: maestroKeyVaultCertificateDomain
    clientName: maestroConsumerName
    clientRole: 'consumer'
    certificateAccessManagedIdentityPrincipalId: maestroServerManagedIdentityPrincipalId
    location: location
  }
}
