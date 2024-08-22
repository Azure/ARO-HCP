/*
This module creates a postgres flexible server, firewall rules, administrators, configurations, and databases.
*/

@description('The name of the Postgres server.')
param name string

param location string = resourceGroup().location

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

param private bool

param managedPrivateEndpoint bool = true

param subnetId string = ''

param vnetId string = ''

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2023-12-01-preview' = {
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
      mode: 'ZoneRedundant'
    }
    maintenanceWindow: maintenanceWindow
    storage: {
      autoGrow: 'Enabled'
      storageSizeGB: storageSizeGB
    }
  }
}

resource postgres_allow_azure_firewall 'Microsoft.DBforPostgreSQL/flexibleServers/firewallRules@2023-12-01-preview' = {
  name: 'AllowAllAzureServicesAndResourcesWithinAzureIps'
  parent: postgres
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '0.0.0.0'
  }
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

@batchSize(1)
resource postgres_database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2023-12-01-preview' = [
  for database in databases: {
    name: database.name
    parent: postgres
    properties: {
      charset: database.charset
      collation: database.collation
    }
    dependsOn: [postgres_config]
  }
]

output hostname string = postgres.properties.fullyQualifiedDomainName
output port int = 5432

//
//   P R I V A T E   E N D P O I N T
//

var privateDnsZoneName = 'privatelink.postgres.database.azure.com'

resource postgresPrivateEndpoint 'Microsoft.Network/privateEndpoints@2024-01-01' = if (managedPrivateEndpoint) {
  name: '${name}-pe'
  location: location
  properties: {
    privateLinkServiceConnections: [
      {
        name: '${name}-pe'
        properties: {
          groupIds: [
            'postgresqlServer'
          ]
          privateLinkServiceId: postgres.id
        }
      }
    ]
    subnet: {
      id: subnetId
    }
  }
  dependsOn: [
    postgres_database
  ]
}

resource postgresPrivateEndpointDnsZone 'Microsoft.Network/privateDnsZones@2020-06-01' = if (managedPrivateEndpoint) {
  name: privateDnsZoneName
  location: 'global'
  properties: {}
  dependsOn: [
    postgresPrivateEndpoint
  ]
}

resource postgresPrivateDnsZoneVnetLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = if (managedPrivateEndpoint) {
  parent: postgresPrivateEndpointDnsZone
  name: 'postgres'
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: vnetId
    }
  }
}

resource privateEndpointDnsGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2023-09-01' = if (managedPrivateEndpoint) {
  parent: postgresPrivateEndpoint
  name: '${name}-dns-group'
  properties: {
    privateDnsZoneConfigs: [
      {
        name: 'config1'
        properties: {
          privateDnsZoneId: postgresPrivateEndpointDnsZone.id
        }
      }
    ]
  }
  dependsOn: [
    postgresPrivateDnsZoneVnetLink
  ]
}
