@description('The name of the AROBit Secret Provider MSI')
param msiName string

param useLeasedInfrastructureIdentities bool = false
param infrastructureIdentityResourceGroup string = ''
param managementIdentityResourceGroups string = ''
@description('Management stamp selecting a leased bundle. Empty for service-cluster lookups.')
param stampIdentifier string = ''

import * as mi from '../modules/managed-identities.bicep'

// Parameter files must only pass values: selection runs after placeholder substitution.
var identityResourceGroup = useLeasedInfrastructureIdentities
  ? (stampIdentifier == ''
      ? infrastructureIdentityResourceGroup
      : mi.getManagementIdentityResourceGroup(managementIdentityResourceGroups, stampIdentifier))
  : resourceGroup().name

//
//   A R O B I T   L O O K U P
//

resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  scope: resourceGroup(identityResourceGroup)
  name: msiName
}

output tenantId string = tenant().tenantId
output msiClientId string = managedIdentity.properties.clientId
