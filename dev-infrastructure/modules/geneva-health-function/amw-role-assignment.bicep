@description('Whether to deploy the Geneva Health Function App')
param deploy bool

@description('Name of the AMW account')
param amwAccountName string

@description('Principal ID to grant the role to')
param managedIdentityPrincipalId string

var monitoringDataReaderRoleId = 'b0d8363b-8ddd-447d-831f-62ca05bff136'

resource hcpAmw 'microsoft.monitor/accounts@2021-06-03-preview' existing = if (deploy) {
  name: amwAccountName
}

resource amwRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (deploy) {
  name: guid(managedIdentityPrincipalId, hcpAmw.id, monitoringDataReaderRoleId)
  scope: hcpAmw
  properties: {
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', monitoringDataReaderRoleId)
  }
}
