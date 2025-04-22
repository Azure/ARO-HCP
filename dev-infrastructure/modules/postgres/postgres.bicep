/*
This module creates a postgres flexible server, firewall rules, administrators, configurations, and databases.
*/

@description('The name of the Postgres server.')
param name string

param location string = resourceGroup().location

param sku string = 'Standard_D2s_v3'
param tier string = 'GeneralPurpose'
param minTLSVersion string

type DatabaseAdministrators = {
  principalId: string
  principalName: string
  principalType: string
}

@description('The database administrators to create on the server.')
param databaseAdministrators DatabaseAdministrators[] = []

type DatabaseConfigurations = {
  source: string
  value: string
}
@description('The configuration options to set on the server.')
param configurations DatabaseConfigurations[] = []

type DatabaseProperties = {
  name: string
  charset: string
  collation: string
}
@description('The databases to create on the server.')
param databases DatabaseProperties[] = []

@description('The zone redundant mode of the Postgres Database')
param postgresZoneRedundantMode string

type MaintenanceWindow = {
  customWindow: string
  dayOfWeek: int
  startHour: int
  startMinute: int
}
@description('The maintenance window for the server.')
param maintenanceWindow MaintenanceWindow

@description('The number of days to retain backups for.')
param backupRetentionDays int = 7

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
param storageSizeGB int

@description('The log analytics workspace ID to link to the server.')
param logAnalyticsWorkspaceId string = ''

param version string

param private bool

param managedPrivateEndpoint bool = true
param managedPrivateEndpointResourceGroup string = resourceGroup().name

param subnetId string = ''

param vnetId string = ''

@secure()
@description('The administrator login password (required for server creation).')
param administratorLoginPassword string = ''

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2023-12-01-preview' = {
  name: name
  location: resourceGroup().location
  sku: {
    name: sku
    tier: tier
  }
  properties: {
    administratorLogin: ''
    administratorLoginPassword: administratorLoginPassword
    version: version
    createMode: 'Default'
    network: {
      publicNetworkAccess: private ? 'Disabled' : 'Enabled'
    }
    authConfig: {
      activeDirectoryAuth: 'Enabled'
      passwordAuth: 'Disabled'
      tenantId: subscription().tenantId
    }
    backup: {
      backupRetentionDays: backupRetentionDays
      geoRedundantBackup: 'Disabled'
    }
    dataEncryption: {
      type: 'SystemManaged'
    }
    highAvailability: {
      mode: postgresZoneRedundantMode
    }
    maintenanceWindow: maintenanceWindow
    storage: {
      autoGrow: 'Enabled'
      storageSizeGB: storageSizeGB
    }
  }
}

resource postgres_allow_public_access 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2023-12-01-preview' = if (!private) {
  name: 'AllowPublicAccess'
  parent: postgres
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '255.255.255.255'
  }
}

resource postgres_allow_azure_firewall 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2023-12-01-preview' = {
  name: 'AllowAllAzureServicesAndResourcesWithinAzureIps'
  parent: postgres
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '0.0.0.0'
  }
  dependsOn: [postgres_allow_public_access]
}

@batchSize(1)
resource postgres_admin 'Microsoft.DBforPostgreSQL/flexibleServers/administrators@2023-12-01-preview' = [
  for admin in filter(databaseAdministrators, a => a.principalId != ''): {
    name: admin.principalId
    parent: postgres
    properties: {
      principalName: admin.principalName
      principalType: admin.principalType
      tenantId: subscription().tenantId
    }
    dependsOn: [postgres_allow_azure_firewall]
  }
]

@batchSize(1)
resource postgres_config 'Microsoft.DBforPostgreSQL/flexibleServers/configurations@2023-12-01-preview' = [
  for config in configurations: {
    name: config.source
    parent: postgres
    properties: {
      source: 'user-override'
      value: config.value
    }
    dependsOn: [postgres_admin]
  }
]

resource postgres_min_tls 'Microsoft.DBforPostgreSQL/flexibleServers/configurations@2023-12-01-preview' = {
  name: 'ssl_min_protocol_version'
  parent: postgres
  properties: {
    source: 'user-override'
    value: minTLSVersion
  }
  dependsOn: [postgres_config]
}

@batchSize(1)
resource postgres_database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2023-12-01-preview' = [
  for database in databases: {
    name: database.name
    parent: postgres
    properties: {
      charset: database.charset
      collation: database.collation
    }
    dependsOn: [postgres_min_tls]
  }
]

output hostname string = postgres.properties.fullyQualifiedDomainName
output port int = 5432

//
//   P R I V A T E   E N D P O I N T
//

module servicePostgresPrivateEndpoint '../private-endpoint.bicep' = if (managedPrivateEndpoint) {
  name: '${deployment().name}-svcs-kv-pe'
  scope: resourceGroup(managedPrivateEndpointResourceGroup)
  params: {
    location: location
    subnetIds: [subnetId]
    vnetId: vnetId
    privateLinkServiceId: postgres.id
    serviceType: 'postgres'
    groupId: 'postgresqlServer'
  }
  dependsOn: [
    postgres_database
  ]
}

//
//   L O G   A N A L Y T I C S
//

resource aksDiagnosticSettings 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (logAnalyticsWorkspaceId != '') {
  scope: postgres
  name: name
  properties: {
    metrics: [
      {
        category: 'AllMetrics'
        enabled: true
      }
    ]
    logs: [
      {
        categoryGroup: 'allLogs'
        enabled: true
      }
    ]
    workspaceId: logAnalyticsWorkspaceId
  }
}
