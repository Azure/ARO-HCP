param zoneName string
param zoneContributerManagedIdentityPrincipalId string

resource zone 'Microsoft.Network/dnsZones@2018-05-01' existing = {
  name: zoneName
}

var dnsZoneContributorRoleId = 'befefa01-2a29-4197-83a8-272ff33ce314'

resource dnsZoneRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(zone.id, dnsZoneContributorRoleId, zoneContributerManagedIdentityPrincipalId)
  scope: zone
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dnsZoneContributorRoleId)
    principalId: zoneContributerManagedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}
