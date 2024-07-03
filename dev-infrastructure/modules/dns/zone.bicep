/*
The reason for this to be a module (even though it is very minimalistic) is to
allow the creation of the zone in a different RG than the one from the caller.
*/

@description('Create a DNS zone with this name')
param zoneName string

resource zone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: zoneName
  location: 'global'
}

output nameServers array = zone.properties.nameServers
