@description('The name of the CS managed identity')
param csMIName string

resource csMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: csMIName
  location: resourceGroup().location
}

output cs string = csMSI.id
