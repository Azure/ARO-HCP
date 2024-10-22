@description('Azure Region Location')
param location string = resourceGroup().location

@description('Captures logged in users UID')
param currentUserId string

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('This is a global DNS zone name that will be the parent of regional DNS zones to host ARO HCP customer cluster DNS records')
param baseDNSZoneName string

@description('The resource group to deploy the base DNS zone to')
param baseDNSZoneResourceGroup string = 'global'

param regionalDNSSubdomain string = empty(currentUserId)
  ? location
  : '${location}-${take(uniqueString(currentUserId), 5)}'

// Tags the resource group
resource subscriptionTags 'Microsoft.Resources/tags@2024-03-01' = {
  name: 'default'
  scope: resourceGroup()
  properties: {
    tags: {
      persist: toLower(string(persist))
      deployedBy: currentUserId
    }
  }
}

//
// R E G I O N A L   D N S   Z O N E
//

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
