@description('The managed identity name of the logs')
param logsMSI string

resource prometheusUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: 'prometheus'
}

resource logsUAMI 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' existing = {
  name: logsMSI
}

output prometheusUAMIClientId string = prometheusUAMI.properties.clientId
output clusterLogPrincipalId string = logsUAMI.properties.principalId
