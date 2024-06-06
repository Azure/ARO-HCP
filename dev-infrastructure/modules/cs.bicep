@description('The location for the PostGres DB')
param location string

@description('The managed identity name CS will use to interact with Azure resources')
param clusterServiceManagedIdentityName string

@description('The managed identity CS uses to interact with Azure resources')
param clusterServiceManagedIdentityPrincipalId string

@description('An optional user ID that will get admin access on the Postgres database')
param currentUserId string

@description('An optional user principal name that will get admin access on the Postgres database')
param currentUserPrincipal string

@description('The name of the database to create for CS')
param csDatabaseName string = 'ocm'

@description('The AKS cluster name where cluster-service is hosted.')
param aksClusterName string

@description('The namespace where CS will be hosted.')
param namespace string

var postgresServerName = 'cs-${location}-${uniqueString(resourceGroup().name)}'

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
      // use the current user as DB admin if defined - for dev purposes
      {
        principalId: currentUserId
        principalName: currentUserPrincipal
        principalType: 'User'
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
  }
}

//
// Create DB user for the cluster-service managed identity and enable entra authentication
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
// create configmap with DB access metadata in AKS cluster
//

resource aksCluster 'Microsoft.ContainerService/managedClusters@2024-01-01' existing = {
  name: aksClusterName
}

module dbConfigMap './aks-manifest.bicep' = {
  name: '${deployment().name}-db-configmap'
  params: {
    aksClusterName: aksClusterName
    manifests: [
      {
        apiVersion: 'v1'
        kind: 'ConfigMap'
        metadata: {
          name: 'db'
          namespace: namespace
        }
        data: {
          name: csDatabaseName
          username: clusterServiceManagedIdentityName
          host: postgres.outputs.hostname
          port: string(postgres.outputs.port)
        }
      }
    ]
    aksManagedIdentityId: items(aksCluster.identity.userAssignedIdentities)[0].key
    location: location
  }
}

//
// output
//

output postgresHostname string = postgres.outputs.hostname
output csDatabaseName string = csDatabaseName
output csDatabaseUsername string = clusterServiceManagedIdentityName
