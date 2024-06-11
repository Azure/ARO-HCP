/*
This module creates a postgres flexible server, firewall rules, administrators, configurations, and databases.
TODO: support for private endpoint
*/

@description('The name of the Postgres server.')
param name string

param sku string = 'Standard_D2s_v3'
param tier string = 'GeneralPurpose'

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

param version string

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2023-03-01-preview' = {
  name: name
  location: resourceGroup().location
  sku: {
    name: sku
    tier: tier
  }
  properties: {
    administratorLogin: ''
    administratorLoginPassword: ''
    version: version
    createMode: 'Default'
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
      mode: 'ZoneRedundant'
    }
    maintenanceWindow: maintenanceWindow
    storage: {
      autoGrow: 'Enabled'
      storageSizeGB: storageSizeGB
    }
  }
}

resource postgres_firewall 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2023-03-01-preview' = {
  name: '${name}-allow-all'
  parent: postgres
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '255.255.255.255'
  }
}

resource postgres_admin 'Microsoft.DBforPostgreSQL/flexibleServers/administrators@2023-03-01-preview' = [
  for admin in filter(databaseAdministrators, a => a.principalId != ''): {
    name: admin.principalId
    parent: postgres
    properties: {
      principalName: admin.principalName
      principalType: admin.principalType
      tenantId: subscription().tenantId
    }
  }
]

// to figure out how to run this sequentially
resource postgres_config 'Microsoft.DBforPostgreSQL/flexibleServers/configurations@2023-03-01-preview' = [
  for (config, i) in configurations: {
    name: config.source
    parent: postgres
    properties: {
      source: 'user-override'
      value: config.value
    }
    dependsOn: i == 0
      ? [postgres_admin]
      : [
          resourceId(
            'Microsoft.DBforPostgreSQL/flexibleServers/configurations',
            postgres.name,
            configurations[i - 1].source
          )
        ]
  }
]

resource postgres_database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2023-03-01-preview' = [
  for database in databases: {
    name: database.name
    parent: postgres
    properties: {
      charset: database.charset
      collation: database.collation
    }
  }
]

output hostname string = postgres.properties.fullyQualifiedDomainName
output port int = 5432
