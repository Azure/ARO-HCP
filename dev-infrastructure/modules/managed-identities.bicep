param location string

param manageIdentityNames string[]

@description('Reference identities from identityResourceGroupName in the current subscription instead of creating them.')
param useLeasedIdentities bool = false

@description('Resource group containing pre-created identities in the current subscription. Required when useLeasedIdentities is true.')
param identityResourceGroupName string = ''

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = [
  for name in manageIdentityNames: if (!useLeasedIdentities) {
    location: location
    name: name
  }
]

resource leasedUami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = [
  for name in manageIdentityNames: {
    name: name
    scope: resourceGroup(identityResourceGroupName)
  }
]

output managedIdentities array = [
  for i in range(0, length(manageIdentityNames)): {
    uamiID: useLeasedIdentities ? leasedUami[i].id : uami[i]!.id
    uamiName: manageIdentityNames[i]
    uamiClientID: useLeasedIdentities ? leasedUami[i].properties.clientId : uami[i]!.properties.clientId
    uamiPrincipalID: useLeasedIdentities ? leasedUami[i].properties.principalId : uami[i]!.properties.principalId
  }
]

@export()
type managedIdentity = {
  uamiID: string
  uamiName: string
  uamiClientID: string
  uamiPrincipalID: string
}

@export()
func getManagedIdentityByName(managedIdentities array, identityName string) managedIdentity =>
  filter(managedIdentities, id => id.uamiName == identityName)[0]

@export()
func getManagementIdentityResourceGroup(managementResourceGroups string, stampIdentifier string) string =>
  split(filter(split(managementResourceGroups, ','), mapping => startsWith(mapping, '${stampIdentifier}='))[0], '=')[1]
