@description('The name of the CS managed identity')
param csMIName string

resource csMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: csMIName
}

output cs string = csMSI.id

output subscriptionId string = subscription().id
