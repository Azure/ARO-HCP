@description('The managed identity name CS will use to interact with Azure resources')
param clusterServiceManagedIdentityName string

@description('The managed identity CS uses to interact with Azure resources')
param clusterServiceManagedIdentityPrincipalId string

@description('Defines if the Postgres server should be deployed')
param deployPostgres bool

@description('The name of the database to create for CS')
param csDatabaseName string

@description('The name of the Postgres server for CS')
param postgresServerName string

@description('The minimum TLS version for the Postgres server')
param postgresServerMinTLSVersion string

@description('The version of the Postgres server for CS')
param postgresServerVersion string

@description('The size of the Postgres server storage for CS')
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

@description('Defines if the Postgres server is private')
param postgresServerPrivate bool

@description('The subnet ID for the private endpoint of the Postgres server')
param privateEndpointSubnetId string = ''

@description('The VNET ID for the private endpoint of the Postgres server')
param privateEndpointVnetId string = ''

@description('The resource group for the private endpoint of the Postgres server')
param privateEndpointResourceGroup string = ''

@description('The name of the service keyvault')
param serviceKeyVaultName string

@description('The resource group of the service keyvault')
param serviceKeyVaultResourceGroup string

@description('''
  The regional DNS zone to hold ARO HCP customer cluster DNS records.
  CS requires write access to this zone to provision the DNS records for HCPs.
  ''')
param regionalCXDNSZoneName string

@description('The regional resourece group')
param regionalResourceGroup string

@description('The OCP ACR resource ID. CS will manage tokens for HCPs in this ACR')
param ocpAcrResourceId string

@description('The resource ID of the managed identity used to manage the Postgres server')
param postgresAdministrationManagedIdentityId string

@description('The zone redundant mode of the Postgres Database')
param postgresZoneRedundantMode string

@description('The number of days to retain backups for.')
param postgresBackupRetentionDays int

@description('Enable geo-redundant backups for the PostgreSQL server.')
param postgresGeoRedundantBackup bool

//
//   P O S T G R E S
//

import * as res from 'resource.bicep'

module csPostgres 'postgres/postgres.bicep' = if (deployPostgres) {
  name: 'cs-postgres-deployment'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    name: postgresServerName
    postgresZoneRedundantMode: postgresZoneRedundantMode
    databaseAdministrators: [
      {
        principalId: reference(postgresAdministrationManagedIdentityId, '2023-01-31').principalId
        principalName: res.msiRefFromId(postgresAdministrationManagedIdentityId).name
        principalType: 'ServicePrincipal'
      }
    ]
    version: postgresServerVersion
    minTLSVersion: postgresServerMinTLSVersion
    configurations: [
      // some configs taked over from the CS RDS instance
      // https://gitlab.cee.redhat.com/service/app-interface/-/blob/fc95453b1e0eaf162089525f5b94b6dc1e6a091f/resources/terraform/resources/ocm/clusters-service-production-rds-parameter-group-pg12.yml
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
        name: csDatabaseName
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
    backupRetentionDays: postgresBackupRetentionDays
    geoRedundantBackup: postgresGeoRedundantBackup
    storageSizeGB: postgresServerStorageSizeGB
    private: postgresServerPrivate
    subnetId: privateEndpointSubnetId
    vnetId: privateEndpointVnetId
    managedPrivateEndpoint: true
    managedPrivateEndpointResourceGroup: privateEndpointResourceGroup
  }
}

//
// Create DB user for the clusters-service managed identity and enable entra authentication
//

module csManagedIdentityDatabaseAccess 'postgres/postgres-access.bicep' = if (deployPostgres) {
  name: 'cs-db-access'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    postgresServerName: postgresServerName
    postgresAdministrationManagedIdentityId: postgresAdministrationManagedIdentityId
    databaseName: csDatabaseName
    newUserName: clusterServiceManagedIdentityName
    newUserPrincipalId: clusterServiceManagedIdentityPrincipalId
  }
  dependsOn: [
    csPostgres
  ]
}
