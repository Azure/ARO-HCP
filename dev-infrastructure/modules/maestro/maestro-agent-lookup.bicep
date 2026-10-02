@description('The name of the Maestro Agent MSI')
param msiName string

param useLeasedInfrastructureIdentities bool = false
param managementIdentityResourceGroups string = ''
param stampIdentifier string
import * as mi from '../managed-identities.bicep'
var identityResourceGroup = mi.getManagementIdentityResourceGroup(managementIdentityResourceGroups, stampIdentifier)
var identityScope = resourceGroup(useLeasedInfrastructureIdentities ? identityResourceGroup : resourceGroup().name)

//
//   M A E S T R O   A G E N T   L O O K U P
//

resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: identityScope
  name: msiName
}

output tenantId string = tenant().tenantId
output msiClientId string = managedIdentity.properties.clientId
