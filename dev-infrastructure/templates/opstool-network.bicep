import {
  csvToArray
  getLocationAvailabilityZonesCSV
} from '../modules/common.bicep'

@description('Location of the shared opstool network resources')
param location string = resourceGroup().location

@description('AKS cluster name')
param aksClusterName string

@description('Enable or disable gateway-facing network resources')
param gatewayEnabled bool = true

@description('Static public IP name for the shared opstool gateway')
@minLength(5)
param publicIpName string

@description('Parent DNS zone name that will delegate to the opstool child zone')
param parentZoneName string

@description('Resource group containing the parent DNS zone')
param parentZoneResourceGroupName string

@description('Child zone subdomain for opstool')
param childZoneSubdomain string

@description('DNS A record name in the child zone; use * for a wildcard apex (*.zone)')
param recordName string

@description('Owning team tag value')
param owningTeamTagValue string = 'ARO-HCP-SRE'

var networkContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions/',
  '4d97b98b-1d4f-4787-a291-c67834d212e7'
)
var locationAvailabilityZoneList = csvToArray(getLocationAvailabilityZonesCSV(location))
var childZoneName = '${childZoneSubdomain}.${parentZoneName}'
var nodeSubnetNsgName = 'opstool-cluster-nsg'
var publicIpTags = {
  persist: 'true'
  purpose: 'opstool-gateway'
  owningTeam: owningTeamTagValue
}

module managedIdentities '../modules/managed-identities.bicep' = {
  name: 'opstool-managed-identities'
  params: {
    location: location
    manageIdentityNames: [
      'opstool'
      'cihealth'
      'cert-manager'
      'prometheus'
      'tenant-quota'
    ]
  }
}

var managedIdentityOutputs = managedIdentities.outputs.managedIdentities
var certManagerMI = filter(managedIdentityOutputs, id => id.uamiName == 'cert-manager')[0]

resource aksClusterUserDefinedManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${aksClusterName}-msi'
  location: location
}

resource nodeSubnetNSG 'Microsoft.Network/networkSecurityGroups@2023-11-01' = {
  name: nodeSubnetNsgName
  location: location
  tags: {
    persist: 'true'
    owningTeam: owningTeamTagValue
  }
}

module gatewayPublicIP '../modules/network/publicipaddress.bicep' = if (gatewayEnabled) {
  name: publicIpName
  params: {
    name: publicIpName
    location: location
    zones: length(locationAvailabilityZoneList) > 0 ? locationAvailabilityZoneList : null
    tags: publicIpTags
    roleAssignmentProperties: {
      principalId: aksClusterUserDefinedManagedIdentity.properties.principalId
      principalType: 'ServicePrincipal'
      roleDefinitionId: networkContributorRoleId
    }
  }
}

resource childZone 'Microsoft.Network/dnsZones@2018-05-01' = if (gatewayEnabled) {
  name: childZoneName
  location: 'global'
}

// NOTE: incremental ARM deployments do not delete previously-used child zones or
// delegations. Clean up the old apps.<regionShort>.hcpsvc.osadev.cloud zone and
// delegation manually after the tools.hcpsvc.osadev.cloud cutover is complete.
module childZoneDelegation '../modules/dns/zone-delegation.bicep' = if (gatewayEnabled) {
  name: '${childZoneSubdomain}-svc-zone-deleg'
  scope: resourceGroup(parentZoneResourceGroupName)
  params: {
    childZoneName: childZoneSubdomain
    childZoneNameservers: childZone!.properties.nameServers
    parentZoneName: parentZoneName
  }
}

module gatewayDNS '../modules/dns/a-record.bicep' = if (gatewayEnabled) {
  name: 'opstool-gateway-dns'
  params: {
    zoneName: childZone!.name
    recordName: recordName
    ipAddress: gatewayPublicIP!.outputs.ipAddress
    ttl: 300
  }
}

module certManagerDnsZoneContributor '../modules/dns/zone-contributor.bicep' = if (gatewayEnabled) {
  name: 'cert-manager-dns-zone-contributor'
  params: {
    zoneName: childZone!.name
    zoneContributerManagedIdentityPrincipalIds: [certManagerMI.uamiPrincipalID]
  }
}

resource gatewayHTTPSInboundRule 'Microsoft.Network/networkSecurityGroups/securityRules@2023-11-01' = if (gatewayEnabled) {
  parent: nodeSubnetNSG
  name: 'opstool-gateway-443-in-internet'
  properties: {
    access: 'Allow'
    destinationAddressPrefix: gatewayPublicIP!.outputs.ipAddress
    destinationPortRange: '443'
    direction: 'Inbound'
    priority: 130
    protocol: 'Tcp'
    sourceAddressPrefix: 'Internet'
    sourcePortRange: '*'
  }
}

output gatewayPublicIpName string = gatewayEnabled ? publicIpName : ''
output gatewayPublicIpAddress string = gatewayEnabled ? gatewayPublicIP!.outputs.ipAddress : ''
output gatewayPublicIpResourceId string = gatewayEnabled ? gatewayPublicIP!.outputs.resourceId : ''
output gatewayDnsZoneName string = gatewayEnabled ? childZone!.name : ''
output gatewayWildcardHostname string = gatewayEnabled ? '*.${childZone!.name}' : ''
