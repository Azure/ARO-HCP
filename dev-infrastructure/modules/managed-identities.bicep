param location string

param manageIdentityNames string[]

resource uami 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = [
  for name in manageIdentityNames: {
    location: location
    name: name
  }
]

output managedIdentities array = [
  for i in range(0, length(manageIdentityNames)): {
    uamiID: uami[i].id
    uamiName: manageIdentityNames[i]
    uamiClientID: uami[i].properties.clientId
    uamiPrincipalID: uami[i].properties.principalId
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
