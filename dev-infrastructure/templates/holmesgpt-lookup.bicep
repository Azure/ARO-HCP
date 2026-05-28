@description('The name of the HolmesGPT MSI')
param holmesgptMsiName string

resource holmesgptIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup()
  name: holmesgptMsiName
}

output holmesgptMsiClientId string = holmesgptIdentity.properties.clientId
output tenantId string = holmesgptIdentity.properties.tenantId
