@description('The name of the Exporter Secret Provider MSI')
param msiName string

//
//   E X P O R T E R   L O O K U P
//

resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: msiName
}

output tenantId string = tenant().tenantId
output msiClientId string = managedIdentity.properties.clientId
output exporterPrincipalId string = managedIdentity.properties.principalId
