param parentZoneName string

param childZoneName string = resourceGroup().location

param childZoneNameservers array

param ttl int = 3600

resource parentZone 'Microsoft.Network/dnsZones@2018-05-01' existing = {
  name: parentZoneName
}

resource delegation 'Microsoft.Network/dnsZones/NS@2018-05-01' = {
  parent: parentZone
  name: childZoneName
  properties: {
    TTL: ttl
    NSRecords: [
      for ns in childZoneNameservers: {
        nsdname: ns
      }
    ]
  }
}
