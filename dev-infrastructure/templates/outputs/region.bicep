resource csMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'clusters-service'
  location: resourceGroup().location
}

output cs string = csMSI.id
