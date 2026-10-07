@description('The name of the Exporter Secret Provider MSI')
param msiName string

param useLeasedInfrastructureIdentities bool = false
param infrastructureIdentityResourceGroup string = ''
var identityScope = resourceGroup(useLeasedInfrastructureIdentities
  ? infrastructureIdentityResourceGroup
  : resourceGroup().name)

//
//   E X P O R T E R   L O O K U P
//

resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: identityScope
  name: msiName
}

output tenantId string = tenant().tenantId
output msiClientId string = managedIdentity.properties.clientId
output exporterPrincipalId string = managedIdentity.properties.principalId
