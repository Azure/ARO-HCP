/*
This module is responsible for:
 - setting up EventGrid access for the maestro server

Execution scope: the resourcegroup of the AKS cluster where the maestro server
will be deployed.

TODO:
- once Key Vault and EventGrid have network access restrictions enabled,
  this module needs to be enhanced to manage access to both (e.g. privatelink)
*/

param maestroInfraResourceGroup string
param maestroEventGridNamespaceName string
param maestroKeyVaultName string
param maestroKeyVaultOfficerManagedIdentityName string
param maestroKeyVaultCertificateDomain string

@description('Whether to deploy the Postgres server for Maestro')
param deployPostgres bool

@description('The name of the Postgres server for Maestro')
param postgresServerName string

@description('The version of the Postgres server for Maestro')
param postgresServerVersion string

@description('The size of the Postgres server storage for Maestro')
@allowed([
  32
  64
  128
  256
  512
  1024
  2048
  4096
  8192
  16384
  32768
])
param postgresServerStorageSizeGB int

param postgresServerPrivate bool

param privateEndpointSubnetId string = ''

param privateEndpointVnetId string = ''

@description('The name of the database to create for Maestro')
param maestroDatabaseName string = 'maestro'

@description('The name of the Managed Identity for the Maestro cluster service')
param maestroServerManagedIdentityName string

@description('The principal ID of the Managed Identity for the Maestro cluster service')
param maestroServerManagedIdentityPrincipalId string

param location string

//
//   P O S T G R E S
//

resource postgresAdminManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${postgresServerName}-db-admin-msi'
  location: location
}

module postgres '../postgres/postgres.bicep' = if (deployPostgres) {
  name: '${deployment().name}-postgres'
  params: {
    name: postgresServerName
    databaseAdministrators: [
      // add the dedicated admin managed identity as administrator
      // this one is going to be used to manage DB access
      {
        principalId: postgresAdminManagedIdentity.properties.principalId
        principalName: postgresAdminManagedIdentity.name
        principalType: 'ServicePrincipal'
      }
    ]
    version: postgresServerVersion
    configurations: [
      {
        source: 'log_min_duration_statement'
        value: '3000'
      }
      {
        source: 'log_statement'
        value: 'all'
      }
    ]
    databases: [
      {
        name: maestroDatabaseName
        charset: 'UTF8'
        collation: 'en_US.utf8'
      }
    ]
    maintenanceWindow: {
      customWindow: 'Enabled'
      dayOfWeek: 0
      startHour: 1
      startMinute: 12
    }
    storageSizeGB: postgresServerStorageSizeGB
    private: postgresServerPrivate
    subnetId: privateEndpointSubnetId
    vnetId: privateEndpointVnetId
    managedPrivateEndpoint: true
  }
}

module csManagedIdentityDatabaseAccess '../postgres/postgres-access.bicep' = if (deployPostgres) {
  name: '${deployment().name}-maestro-db-access'
  params: {
    postgresServerName: postgresServerName
    postgresAdminManagedIdentityName: postgresAdminManagedIdentity.name
    databaseName: maestroDatabaseName
    newUserName: maestroServerManagedIdentityName
    newUserPrincipalId: maestroServerManagedIdentityPrincipalId
  }
  dependsOn: [
    postgres
  ]
}

//
//   E V E N T G R I D
//

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
