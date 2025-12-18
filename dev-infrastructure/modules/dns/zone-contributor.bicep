param zoneName string
param zoneContributerManagedIdentityPrincipalIds array

resource zone 'Microsoft.Network/dnsZones@2018-05-01' existing = {
  name: zoneName
}

var dnsZoneContributorRoleId = 'befefa01-2a29-4197-83a8-272ff33ce314'

resource dnsZoneRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = [
  for principalId in zoneContributerManagedIdentityPrincipalIds: {
    name: guid(zone.id, dnsZoneContributorRoleId, principalId)
    scope: zone
    properties: {
      roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dnsZoneContributorRoleId)
      principalId: principalId
      principalType: 'ServicePrincipal'
    }
  }
]
