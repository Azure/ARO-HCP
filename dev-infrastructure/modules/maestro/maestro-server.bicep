/*
This module is responsible for:
 - setting up Postgres access for the maestro server
 - setting up EventGrid access for the maestro server

Execution scope: the resourcegroup of the AKS cluster where the maestro server
will be deployed.
*/

param maestroInfraResourceGroup string
param maestroEventGridNamespaceName string
param certKeyVaultName string
param certKeyVaultResourceGroup string
param keyVaultOfficerManagedIdentityName string
param maestroCertificateDomain string
param maestroCertificateIssuer string

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

param postgresServerPrivate bool

param privateEndpointSubnetId string = ''

param privateEndpointVnetId string = ''

param privateEndpointResourceGroup string = ''

@description('The name of the database to create for Maestro')
param maestroDatabaseName string

@description('The name of the Managed Identity for the Maestro cluster service')
param maestroServerManagedIdentityName string

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

@description('The regional resource group')
param regionalResourceGroup string

//
//   P O S T G R E S
//

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

module maestroManagedIdentityDatabaseAccess '../postgres/postgres-access.bicep' = if (deployPostgres) {
  name: 'maestro-db-access'
  scope: resourceGroup(regionalResourceGroup)
  params: {
    postgresServerName: postgresServerName
    postgresAdministrationManagedIdentityId: postgresAdministrationManagedIdentityId
    databaseName: maestroDatabaseName
    newUserName: maestroServerManagedIdentityName
    newUserPrincipalId: maestroServerManagedIdentityPrincipalId
  }
  dependsOn: [
    maestroPostgres
  ]
}

//
//   E V E N T G R I D   A C C E S S
//

module eventGridClientCert '../keyvault/key-vault-cert-with-access.bicep' = {
  name: 'maestro-eg-crt-${uniqueString(mqttClientName)}'
  scope: resourceGroup(certKeyVaultResourceGroup)
  params: {
    keyVaultName: certKeyVaultName
    kvCertOfficerManagedIdentityResourceId: keyVaultOfficerManagedIdentityName
    certDomain: maestroCertificateDomain
    certificateIssuer: maestroCertificateIssuer
    hostName: mqttClientName
    keyVaultCertificateName: mqttClientName
    certificateAccessManagedIdentityPrincipalId: maestroServerManagedIdentityPrincipalId
  }
}

module evengGridAccess 'maestro-eventgrid-access.bicep' = {
  name: 'maestro-eg-access-${uniqueString(mqttClientName)}'
  scope: resourceGroup(maestroInfraResourceGroup)
  params: {
    eventGridNamespaceName: maestroEventGridNamespaceName
    clientName: mqttClientName
    clientRole: 'server'
    certificateThumbprint: eventGridClientCert.outputs.certificateThumbprint
    certificateSAN: eventGridClientCert.outputs.certificateSAN
    certificateIssuer: maestroCertificateIssuer
  }
}
