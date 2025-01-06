@description('Azure Region Location')
param location string = resourceGroup().location

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The maximum client sessions per authentication name for the EventGrid MQTT broker')
param maestroEventGridMaxClientSessionsPerAuthName int

@description('Allow/deny public network access to the Maestro EventGrid Namespace')
param maestroEventGridPrivate bool

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('This is a global DNS zone name that will be the parent of regional DNS zones to host ARO HCP customer cluster DNS records')
param baseDNSZoneName string

@description('The resource group to deploy the base DNS zone to')
param baseDNSZoneResourceGroup string = 'global'

param regionalDNSSubdomain string

param globalRegion string
param regionalRegion string
param globalResourceGroup string
param ocpAcrName string
param svcAcrName string

// Tags the resource group
resource subscriptionTags 'Microsoft.Resources/tags@2024-03-01' = {
  name: 'default'
  scope: resourceGroup()
  properties: {
    tags: {
      persist: toLower(string(persist))
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
  name: '${deployment().name}-zone-deleg'
  scope: resourceGroup(baseDNSZoneResourceGroup)
  params: {
    childZoneName: regionalDNSSubdomain
    childZoneNameservers: regionalZone.properties.nameServers
    parentZoneName: baseDNSZoneName
  }
}

//
// R E G I O N A L   A C R   R E P L I C A T I O N
//

var ocpAcrReplicationName = '${ocpAcrName}${location}replica'
module ocpAcrReplication '../modules/acr/acr-replication.bicep' = if (globalRegion != regionalRegion) {
  name: ocpAcrReplicationName
  scope: resourceGroup(globalResourceGroup)
  params: {
    acrReplicationLocation: location
    acrReplicationParentAcrName: ocpAcrName
    acrReplicationReplicaName: ocpAcrReplicationName
  }
}

var svcAcrReplicationName = '${svcAcrName}${location}replica'
module svcAcrReplication '../modules/acr/acr-replication.bicep' = if (globalRegion != regionalRegion) {
  name: svcAcrReplicationName
  scope: resourceGroup(globalResourceGroup)
  params: {
    acrReplicationLocation: location
    acrReplicationParentAcrName: svcAcrName
    acrReplicationReplicaName: svcAcrReplicationName
  }
}

//
// M A E S T R O
//

module maestroInfra '../modules/maestro/maestro-infra.bicep' = {
  name: '${deployment().name}-maestro'
  params: {
    eventGridNamespaceName: maestroEventGridNamespacesName
    location: location
    maxClientSessionsPerAuthName: maestroEventGridMaxClientSessionsPerAuthName
    publicNetworkAccess: maestroEventGridPrivate ? 'Disabled' : 'Enabled'
  }
}
