@description('The global MSI name')
param globalMSIName string

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

output globalMSIId string = globalMSI.id
