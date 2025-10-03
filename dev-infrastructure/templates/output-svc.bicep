@description('The name of the CS managed identity')
param csMIName string

@description('The name of the MSI refresher managed identity')
param msiRefresherMIName string

@description('The name of the OIDC storage account')
param oidcStorageAccountName string

@description('The name of the regional Azure Front Door DNS zone for OIDC')
param regionalAzureFrontDoortDnsZoneName string

// CS MI resource ID
resource csMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: csMIName
}
output cs string = csMSI.id
output csClientId string = csMSI.properties.clientId

// MSI refresher MI resource ID
resource msiRefresherMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: msiRefresherMIName
}
output msiRefresher string = msiRefresherMSI.id

// Image Puller MI
resource imagePullerIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: 'image-puller'
}
output imagePullerId string = imagePullerIdentity.id
output imagePullerClientId string = imagePullerIdentity.properties.clientId

output subscriptionId string = subscription().id
output tenantId string = subscription().tenantId

@description('The resourcegroup for regional infrastructure')
param regionalResourceGroup string

@description('The name of the CS Postgres server')
param csPostgresServerName string

@description('The name of the CS Postgres database')
param csPostgresDatabaseName string

@description('Deploy CS Postgres if true')
param csPostgresDeploy bool

@description('The name of the CS operator roles')
param opClusterApiAzureRoleName string
param opControlPlaneRoleName string
param opCloudControllerManagerRoleName string
param opIngressRoleName string
param opDiskCsiDriverRoleName string
param opFileCsiDriverRoleName string
param opImageRegistryRoleName string
param opCloudNetworkConfigRoleName string
param opKmsRoleName string

//
//   O I D C
//

resource oidcStorageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' existing = {
  name: oidcStorageAccountName
  scope: resourceGroup(regionalResourceGroup)
}

output oidcStorageAccountBlobServiceEndpoint string = oidcStorageAccount.properties.primaryEndpoints.blob
output oidcIssuerBaseEndpoint string = oidcStorageAccount.properties.allowBlobPublicAccess
  ? oidcStorageAccount.properties.primaryEndpoints.web
  : 'https://${regionalAzureFrontDoortDnsZoneName}/'

//
// C L U S T E R   S E R V I C E   D A T A B A S E
//

resource csPostgres 'Microsoft.DBforPostgreSQL/flexibleServers@2023-12-01-preview' existing = if (csPostgresDeploy) {
  name: csPostgresServerName
  scope: resourceGroup(regionalResourceGroup)
}

output csDatabaseHost string = csPostgresDeploy ? csPostgres.properties.fullyQualifiedDomainName : 'ocm-cs-db'
output csDatabaseName string = csPostgresDeploy ? csPostgresDatabaseName : 'ocm-cs-db'
output csDatabaseUser string = csPostgresDeploy ? csMIName : 'ocm'
output csLocalDb bool = !csPostgresDeploy

//
// C L U S T E R   S E R V I C E   O P E R A T O R   R O L E S
//

// todo - lookup role ids via name once bicep supports it
// https://github.com/Azure/bicep/issues/16867
