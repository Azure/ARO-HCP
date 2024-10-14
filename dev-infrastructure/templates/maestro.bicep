@description('Azure Region Location')
param location string = resourceGroup().location

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The resourcegroup for regional infrastructure')
param svcResourceGroup string

@description('The resourcegroup for regional infrastructure')
param mgmtResourceGroup string

@description('The domain to use to use for the maestro certificate. Relevant only for environments where OneCert can be used.')
param maestroCertDomain string

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The maximum client sessions per authentication name for the EventGrid MQTT broker')
param maestroEventGridMaxClientSessionsPerAuthName int

@description('The name of the keyvault for Maestro Eventgrid namespace certificates.')
@maxLength(24)
param maestroKeyVaultName string

@description('Deploy ARO HCP Maestro Postgres if true')
param deployMaestroPostgres bool = true

@description('The name of the Postgres server for Maestro')
@maxLength(60)
param maestroPostgresServerName string

@description('The version of the Postgres server for Maestro')
param maestroPostgresServerVersion string


@description('The size of the Postgres server for Maestro')
param maestroPostgresServerStorageSizeGB int

@description('If true, make the Maestro Postgres instance private')
param maestroPostgresPrivate bool = true

@description('The name of the managed identity that will manage certificates in maestros keyvault.')
param maestroKeyVaultCertOfficerMSIName string = '${maestroKeyVaultName}-cert-officer-msi'

@description('Deploys a Maestro Consumer to the management cluster if set to true.')
param deployMaestroConsumer bool


//
// M A E S T R O  R E G I O N A L
//

module maestroInfra '../modules/maestro/maestro-infra.bicep' = {
  name: 'maestro-infra'
  params: {
    eventGridNamespaceName: maestroEventGridNamespacesName
    location: location
    maxClientSessionsPerAuthName: maestroEventGridMaxClientSessionsPerAuthName
    maestroKeyVaultName: maestroKeyVaultName
    kvCertOfficerManagedIdentityName: maestroKeyVaultCertOfficerMSIName
  }
}

//
//   M A E S T R O  S E R V E R
//

resource vnet 'Microsoft.Network/virtualNetworks@2023-11-01' existing= {
  name: 'aks-net'
  scope: resourceGroup()
}

resource aksNodeSubnet 'Microsoft.Network/virtualNetworks/subnets@2023-11-01' existing= {
  parent: vnet
  name: 'ClusterSubnet-001'
}

var svcWorkloadIdentities = items({
  maestro_wi: {
    uamiName: 'maestro-server'
    namespace: 'maestro'
    serviceAccountName: 'maestro'
  }
})

resource svcUami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing= [
  for wi in svcWorkloadIdentities: {
    name: wi.value.uamiName
    scope: resourceGroup(svcResourceGroup)
  }
]

module maestroServer '../modules/maestro/maestro-server.bicep' = {
  name: 'maestro-server'
  scope: resourceGroup(svcResourceGroup)
  params: {
    maestroInfraResourceGroup: regionalResourceGroup
    maestroEventGridNamespaceName: maestroEventGridNamespacesName
    maestroKeyVaultName: maestroKeyVaultName
    maestroKeyVaultOfficerManagedIdentityName: maestroKeyVaultCertOfficerMSIName
    maestroKeyVaultCertificateDomain: maestroCertDomain
    deployPostgres: deployMaestroPostgres
    postgresServerName: maestroPostgresServerName
    postgresServerVersion: maestroPostgresServerVersion
    postgresServerStorageSizeGB: maestroPostgresServerStorageSizeGB
    privateEndpointSubnetId: aksNodeSubnet.id
    privateEndpointVnetId: vnet.id
    postgresServerPrivate: maestroPostgresPrivate
    maestroServerManagedIdentityPrincipalId: svcUami[0].properties.principalId
    maestroServerManagedIdentityName: 'maestro-server'
    location: location
  }
}

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
func isValidMaestroConsumerName(input string) bool => length(input) <= 90 && contains(input, '[^a-zA-Z0-9_-]') == false

module maestroConsumer '../modules/maestro/maestro-consumer.bicep' = if (deployMaestroConsumer) {
  name: 'maestro-consumer'
  params: {
    maestroServerManagedIdentityPrincipalId: mgmtUami[0].properties.principalId
    maestroInfraResourceGroup: regionalResourceGroup
    maestroConsumerName: isValidMaestroConsumerName(resourceGroup().name) ? mgmtResourceGroup : ''
    maestroEventGridNamespaceName: maestroEventGridNamespacesName
    maestroKeyVaultName: maestroKeyVaultName
    maestroKeyVaultOfficerManagedIdentityName: maestroKeyVaultCertOfficerMSIName
    maestroKeyVaultCertificateDomain: maestroCertDomain
    location: location
  }
}
