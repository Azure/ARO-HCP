@description('The name of the CS managed identity')
param csMIName string

@description('The name of the MSI refresher managed identity')
param msiRefresherMIName string

@description('The name of the Admin API managed identity')
param adminApiMIName string

// CS MI resource ID
resource csMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: csMIName
}

output cs string = csMSI.id

// MSI refresher MI resource ID
resource msiRefresherMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: msiRefresherMIName
}

output msiRefresher string = msiRefresherMSI.id

// Admin API MI resource ID
resource adminApiMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: adminApiMIName
}

output adminApi string = adminApiMSI.id

output subscriptionId string = subscription().id
