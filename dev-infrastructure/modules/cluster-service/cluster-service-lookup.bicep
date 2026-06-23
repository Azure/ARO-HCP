@description('The name of the Image Puller MSI')
param imagePullerMsiName string

@description('The name of the Cluster Service MSI')
param csMsiName string

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The name of the Storage Account used to configure OIDC in ARO-HCP clusters')
param regionalOidcStorageAccountName string

@description('The Azure Front Door OIDC base endpoint, used when blob public access is disabled')
param afdOidcBaseEndpoint string

@description('Whether to use a database in Azure')
param useAzureDB bool

@description('The name of the Postgres server')
param postgresName string

//
//   I M A G E   P U L L E R   L O O K U P
//

resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: imagePullerMsiName
}

output imagePullerMsiClientId string = imagePullerIdentity.properties.clientId

//
//   C L U S T E R   S E R V I C E   L O O K U P
//

resource csIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: csMsiName
}

output tenantId string = tenant().tenantId
output csMsiClientId string = csIdentity.properties.clientId

//
//   O I D C   S T O R A G E   A C C O U N T   L O O K U P
//

resource regionalOidcStorageAccount 'Microsoft.Storage/storageAccounts@2025-06-01' existing = {
  scope: resourceGroup(regionalResourceGroup)
  name: regionalOidcStorageAccountName
}

output oidcIssuerBlobServiceUrl string = regionalOidcStorageAccount.properties.primaryEndpoints.blob
output oidcIssuerBaseUrl string = regionalOidcStorageAccount.properties.allowBlobPublicAccess ? regionalOidcStorageAccount.properties.primaryEndpoints.web : afdOidcBaseEndpoint

//
//   P O S T G R E S
//

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2023-12-01-preview' existing = if (useAzureDB) {
  scope: resourceGroup(regionalResourceGroup)
  name: postgresName
}

output databaseHost string = useAzureDB ? postgres!.properties.fullyQualifiedDomainName : 'ocm-cs-db'
output databaseDisableTls string = useAzureDB ? 'false' : 'true'
output databaseAuthMethod string = useAzureDB ? 'az-entra' : 'postgres'
output deployLocalDatabase string = useAzureDB ? 'false' : 'true'
output databaseName string = useAzureDB ? 'clusters-service' : 'ocm-cs-db'
output databaseUser string = useAzureDB ? 'clusters-service' : 'ocm'
#disable-next-line outputs-should-not-contain-secrets
output databasePassword string = useAzureDB ? '' : 'TheBlurstOfTimes'
