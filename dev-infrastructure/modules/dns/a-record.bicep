param zoneName string
param recordName string
param ipAddress string
param ttl int

resource dnsZone 'Microsoft.Network/dnsZones@2018-05-01' existing = {
  name: zoneName
}

resource frontendDNSRecord 'Microsoft.Network/dnsZones/A@2023-07-01-preview' = {
  name: recordName
  parent: dnsZone
  properties: {
    TTL: ttl
    ARecords: [
      {
        ipv4Address: ipAddress
      }
    ]
  }
}
