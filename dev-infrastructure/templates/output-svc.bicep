@description('The name of the CS managed identity')
param csMIName string

@description('The name of the MSI refresher managed identity')
param msiRefresherMIName string

@description('The name of the Admin API managed identity')
param adminApiMIName string

@description('The name of the Backend managed identity')
param backendMIName string

@description('The name of the Session Gate managed identity')
param sessiongateMIName string

@description('The name of the Fleet managed identity')
param fleetMIName string

@description('The name of the Exporter managed identity')
param exporterMIName string

param useLeasedInfrastructureIdentities bool = false
param infrastructureIdentityResourceGroup string = ''
var identityScope = resourceGroup(useLeasedInfrastructureIdentities
  ? infrastructureIdentityResourceGroup
  : resourceGroup().name)

// CS MI resource ID
resource csMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: csMIName
  scope: identityScope
}

output cs string = csMSI.id
output csManagedIdentityPrincipalId string = csMSI.properties.principalId

// MSI refresher MI resource ID
resource msiRefresherMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: msiRefresherMIName
  scope: identityScope
}

output msiRefresher string = msiRefresherMSI.id

// Admin API MI resource ID
resource adminApiMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: adminApiMIName
  scope: identityScope
}

output adminApi string = adminApiMSI.id

// RP Backend MI resource ID
resource rpBackendMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: backendMIName
  scope: identityScope
}

output backend string = rpBackendMSI.id

// Session Gate MI resource ID
resource sessiongateMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: sessiongateMIName
  scope: identityScope
}

output sessiongate string = sessiongateMSI.id

// Fleet MI resource ID
resource fleetMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: fleetMIName
  scope: identityScope
}

output fleet string = fleetMSI.id

// Exporter MI resource ID
resource exporterMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: exporterMIName
  scope: identityScope
}

output exporterPrincipalId string = exporterMSI.properties.principalId

output subscriptionId string = subscription().id
