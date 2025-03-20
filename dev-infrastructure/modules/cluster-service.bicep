@description('The managed identity name CS will use to interact with Azure resources')
param clusterServiceManagedIdentityName string

@description('The managed identity CS uses to interact with Azure resources')
param clusterServiceManagedIdentityPrincipalId string

@description('Defines if the Postgres server should be deployed')
param deployPostgres bool

@description('The name of the database to create for CS')
param csDatabaseName string = 'clusters-service'

@description('The name of the Postgres server for CS')
param postgresServerName string

@description('The minimum TLS version for the Postgres server')
param postgresServerMinTLSVersion string

@description('Defines if the Postgres server is private')
param postgresServerPrivate bool

@description('The subnet ID for the private endpoint of the Postgres server')
param privateEndpointSubnetId string = ''

@description('The VNET ID for the private endpoint of the Postgres server')
param privateEndpointVnetId string = ''

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

param postgresZoneRedundantMode string

//
//   P O S T G R E S
//

import * as res from 'resource.bicep'

module postgres 'postgres/postgres.bicep' = if (deployPostgres) {
  name: '${deployment().name}-postgres'
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
    version: '12'
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
    storageSizeGB: 128
    private: postgresServerPrivate
    subnetId: privateEndpointSubnetId
    vnetId: privateEndpointVnetId
    managedPrivateEndpoint: true
  }
}

//
// Create DB user for the clusters-service managed identity and enable entra authentication
//

module csManagedIdentityDatabaseAccess 'postgres/postgres-access.bicep' = if (deployPostgres) {
  name: '${deployment().name}-cs-db-access'
  params: {
    postgresServerName: postgresServerName
    postgresAdministrationManagedIdentityId: postgresAdministrationManagedIdentityId
    databaseName: csDatabaseName
    newUserName: clusterServiceManagedIdentityName
    newUserPrincipalId: clusterServiceManagedIdentityPrincipalId
  }
  dependsOn: [
    postgres
  ]
}

//
//   K E Y V A U L T   A C C E S S
//

module csServiceKeyVaultAccess '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(serviceKeyVaultName, 'cs', 'read')
  scope: resourceGroup(serviceKeyVaultResourceGroup)
  params: {
    keyVaultName: serviceKeyVaultName
    roleName: 'Key Vault Secrets User'
    managedIdentityPrincipalId: clusterServiceManagedIdentityPrincipalId
  }
}

//
//   D N S
//

module csDnsZoneContributor '../modules/dns/zone-contributor.bicep' = {
  name: guid(regionalCXDNSZoneName, clusterServiceManagedIdentityPrincipalId)
  scope: resourceGroup(regionalResourceGroup)
  params: {
    zoneName: regionalCXDNSZoneName
    zoneContributerManagedIdentityPrincipalId: clusterServiceManagedIdentityPrincipalId
  }
}

//
//   O C P   A C R   P E R M I S S I O N S
//

var ocpAcrRef = res.acrRefFromId(ocpAcrResourceId)
module acrManageTokenRole '../modules/acr/acr-permissions.bicep' = {
  name: guid(ocpAcrResourceId, resourceGroup().name, 'clusters-service', 'manage-tokens')
  scope: resourceGroup(ocpAcrRef.resourceGroup.subscriptionId, ocpAcrRef.resourceGroup.name)
  params: {
    principalId: clusterServiceManagedIdentityPrincipalId
    grantManageTokenAccess: true
    acrName: ocpAcrRef.name
  }
}
