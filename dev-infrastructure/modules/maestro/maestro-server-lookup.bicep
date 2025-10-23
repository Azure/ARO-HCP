@description('The name of the Maestro Server MSI')
param maestroMsiName string

@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('Whether to use a database in Azure')
param useAzureDB bool

@description('The name of the Postgres server')
param postgresName string

@description('The regional resource group where postgres is deployed')
param regionalResourceGroup string

//
//   M A E S T R O   S E R V E R   L O O K U P
//

resource maestroIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: maestroMsiName
}

output tenantId string = tenant().tenantId
output maestroMsiClientId string = maestroIdentity.properties.clientId

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId

//
//   P O S T G R E S
//

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2023-12-01-preview' existing = if (useAzureDB) {
  scope: resourceGroup(regionalResourceGroup)
  name: postgresName
}

output databaseHost string = useAzureDB ? postgres.properties.fullyQualifiedDomainName : 'maestro-db'
