@description('The region to deploy the DNS zone into')
param location string = resourceGroup().location

@description('This is a global DNS zone name that will be the parent of regional DNS zones to host ARO HCP customer cluster DNS records')
param baseDNSZoneName string

@description('The resource group to deploy the base DNS zone to')
param baseDNSZoneResourceGroup string = 'global'

@description('Captures logged in users UID')
param currentUserId string = ''

@description('This is the region name in dev/staging/production')
param regionalDNSSubdomain string = empty(currentUserId) ? location : '${location}-${take(uniqueString(currentUserId), 5)}'

resource regionalZone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: '${regionalDNSSubdomain}.${baseDNSZoneName}'
  location: 'global'
}

module regionalZoneDelegation '../modules/dns/zone-delegation.bicep' = {
  name: 'regional-zone-delegation'
  scope: resourceGroup(baseDNSZoneResourceGroup)
  params: {
    childZoneName: regionalDNSSubdomain
    childZoneNameservers: regionalZone.properties.nameServers
    parentZoneName: baseDNSZoneName
  }
}
