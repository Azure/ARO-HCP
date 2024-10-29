@description('Azure Region Location')
param location string = resourceGroup().location

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string


@description('The resourcegroup for regional infrastructure')
param mgmtResourceGroup string

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string


@description('Deploys a Maestro Consumer to the management cluster if set to true.')
param deployMaestroConsumer bool

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The name of the keyvault for Maestro Eventgrid namespace certificates.')
@maxLength(24)
param maestroKeyVaultName string

@description('The name of the managed identity that will manage certificates in maestros keyvault.')
param maestroKeyVaultCertOfficerMSIName string = '${maestroKeyVaultName}-cert-officer-msi'

//
//   M A E S T R O  C O N S U M E R
//


var mgmtWorkloadIdentities = items({
  maestro_wi: {
    uamiName: 'maestro-consumer'
    namespace: 'maestro'
    serviceAccountName: 'maestro'
  }
})

resource mgmtUami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing= [
  for wi in mgmtWorkloadIdentities: {
    name: wi.value.uamiName
    scope: resourceGroup(mgmtResourceGroup)
  }
]

module maestroConsumer '../modules/maestro/maestro-consumer.bicep' = if (deployMaestroConsumer) {
  name: 'maestro-consumer'
  params: {
    maestroServerManagedIdentityPrincipalId: mgmtUami[0].properties.principalId
    maestroInfraResourceGroup: regionalResourceGroup
    maestroConsumerName: mgmtResourceGroup
    maestroEventGridNamespaceName: maestroEventGridNamespacesName
    maestroKeyVaultName: maestroKeyVaultName
    maestroKeyVaultOfficerManagedIdentityName: maestroKeyVaultCertOfficerMSIName
    maestroKeyVaultCertificateDomain: maestroCertDomain
    location: location
  }
}
