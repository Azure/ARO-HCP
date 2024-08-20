@description('The location for the PostGres DB')
param location string

@description('The managed identity name CS will use to interact with Azure resources')
param clusterServiceManagedIdentityName string

@description('The managed identity CS uses to interact with Azure resources')
param clusterServiceManagedIdentityPrincipalId string

@description('The name of the database to create for CS')
param csDatabaseName string = 'clusters-service'

@description('The name of the Postgres server for CS')
param postgresServerName string

param postgresServerPrivate bool

param postgresPrivateEndpointSubnetId string = ''

param postgresPrivateEndpointVnetId string = ''

resource postgresAdminManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${postgresServerName}-db-admin-msi'
  location: location
}

module postgres 'postgres/postgres.bicep' = {
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
    version: '12'
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
    subnetId: postgresPrivateEndpointSubnetId
    vnetId: postgresPrivateEndpointVnetId
    managedPrivateEndpoint: true
  }
}

//
// Create DB user for the clusters-service managed identity and enable entra authentication
//

module csManagedIdentityDatabaseAccess 'postgres/postgres-access.bicep' = {
  name: '${deployment().name}-cs-db-access'
  params: {
    postgresServerName: postgresServerName
    postgresAdminManagedIdentityName: postgresAdminManagedIdentity.name
    databaseName: csDatabaseName
    newUserName: clusterServiceManagedIdentityName
    newUserPrincipalId: clusterServiceManagedIdentityPrincipalId
  }
  dependsOn: [
    postgres
  ]
}

//
// output
//

output postgresHostname string = postgres.outputs.hostname
output csDatabaseName string = csDatabaseName
output csDatabaseUsername string = clusterServiceManagedIdentityName
