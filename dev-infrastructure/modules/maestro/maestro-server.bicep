/*
This module is responsible for:
 - setting up Postgres access for the maestro server
 - setting up EventGrid access for the maestro server
 - granting access to the maestro server certificate in Key Vault

Execution scope: the resourcegroup of the AKS cluster where the maestro server
will be deployed.
*/

param maestroInfraResourceGroup string
param maestroEventGridNamespaceName string
param certKeyVaultName string
param certKeyVaultResourceGroup string
param certKeyVaultSubscription string = subscription().subscriptionId

@description('The subject alternative name of the certificate')
param certificateSAN string

@description('The issuer of the certificate.')
param certificateIssuer string

@description('The name of the MQTT client that will be created in the EventGrid Namespace')
param mqttClientName string

@description('Whether to deploy the Postgres server for Maestro')
param deployPostgres bool

@description('The name of the Postgres server for Maestro')
param postgresServerName string

@description('The version of the Postgres server for Maestro')
param postgresServerMinTLSVersion string

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

@description('The SKU of the Postgres server for Maestro')
param postgresServerSku string

param postgresServerPrivate bool

param privateEndpointSubnetId string = ''

param privateEndpointVnetId string = ''

param privateEndpointResourceGroup string = ''

@description('The name of the database to create for Maestro')
param maestroDatabaseName string

@description('The principal ID of the Managed Identity for the Maestro cluster service')
param maestroServerManagedIdentityPrincipalId string

@description('The resource ID of the managed identity used to manage the Postgres server')
param postgresAdministrationManagedIdentityId string

@description('The zone redundant mode of the Postgres Database')
param postgresZoneRedundantMode string

@description('The number of days to retain backups for.')
param postgresBackupRetentionDays int

@description('Enable geo-redundant backups for the PostgreSQL server.')
param postgresGeoRedundantBackup bool

@description('Enable enhanced metrics for the PostgreSQL server.')
param postgresEnhancedMetricsEnabled bool

@description('The regional resource group')
param regionalResourceGroup string

//
//   P O S T G R E S
//

var baseDBConfigurations = [
  {
    source: 'log_min_duration_statement'
    value: '3000'
  }
  {
    source: 'log_statement'
    value: 'all'
  }
]

var dbEnhancedMetricsConfiguration = [
  {
    // Related to Postgres Enhanced Metrics.
    // https://learn.microsoft.com/en-us/azure/postgresql/monitor/concepts-monitoring#enhanced-metrics
    // Required to be able to have additional postgres metrics available.
    source: 'metrics.collector_database_activity'
    value: 'on'
  }
]

var dbConfigurations = concat(
  baseDBConfigurations,
  postgresEnhancedMetricsEnabled ? dbEnhancedMetricsConfiguration : []
)

import * as res from '../resource.bicep'

module maestroPostgres '../postgres/postgres.bicep' = if (deployPostgres) {
  name: 'maestro-postgres-deployment'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    name: postgresServerName
    postgresZoneRedundantMode: postgresZoneRedundantMode
    minTLSVersion: postgresServerMinTLSVersion
    databaseAdministrators: [
      // add the dedicated admin managed identity as administrator
      // this one is going to be used to manage DB access
      {
        principalId: reference(postgresAdministrationManagedIdentityId, '2023-01-31').principalId
        principalName: res.msiRefFromId(postgresAdministrationManagedIdentityId).name
        principalType: 'ServicePrincipal'
      }
    ]
    version: postgresServerVersion
    configurations: dbConfigurations
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
    backupRetentionDays: postgresBackupRetentionDays
    geoRedundantBackup: postgresGeoRedundantBackup
    storageSizeGB: postgresServerStorageSizeGB
    sku: postgresServerSku
    private: postgresServerPrivate
    subnetId: privateEndpointSubnetId
    vnetId: privateEndpointVnetId
    managedPrivateEndpoint: true
    managedPrivateEndpointResourceGroup: privateEndpointResourceGroup
  }
}

// The maestro-server managed identity is granted database access and enabled
// for Entra authentication by the `maestro-postgres-access` Shell step in
// svc-pipeline.yaml (the deploymentScript-based modules were removed for
// SFI-ID4.2.1 compliance).

//
//   C E R T I F I C A T E   A C C E S S
//

module certSecretAccess '../keyvault/key-vault-secret-access.bicep' = {
  name: 'maestro-cert-access-${uniqueString(mqttClientName)}'
  scope: resourceGroup(certKeyVaultSubscription, certKeyVaultResourceGroup)
  params: {
    keyVaultName: certKeyVaultName
    secretName: mqttClientName
    principalId: maestroServerManagedIdentityPrincipalId
  }
}

//
//   C E R T I F I C A T E   T H U M B P R I N T
//

resource certKv 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  scope: resourceGroup(certKeyVaultSubscription, certKeyVaultResourceGroup)
  name: certKeyVaultName
}

resource certSecret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' existing = {
  parent: certKv
  name: mqttClientName
}

//
//   E V E N T G R I D   A C C E S S
//

module evengGridAccess 'maestro-eventgrid-access.bicep' = {
  name: 'maestro-eg-access-${uniqueString(mqttClientName)}'
  scope: resourceGroup(maestroInfraResourceGroup)
  params: {
    eventGridNamespaceName: maestroEventGridNamespaceName
    clientName: mqttClientName
    clientRole: 'server'
    certificateSAN: certificateSAN
    certificateIssuer: certificateIssuer
    certificateThumbprint: certSecret.?tags.?thumbprint ?? ''
  }
}
