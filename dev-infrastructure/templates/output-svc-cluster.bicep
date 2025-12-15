@description('The managed identity name of the logs')
param logsMSI string

@description('The name of the Admin API managed identity')
param adminApiMIName string

resource prometheusUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'prometheus'
}

resource logsUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: logsMSI
}

resource adminApiUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: adminApiMIName
}

output prometheusUAMIClientId string = prometheusUAMI.properties.clientId
output clusterLogPrincipalId string = logsUAMI.properties.principalId
output adminApiPrincipalId string = adminApiUAMI.properties.principalId
