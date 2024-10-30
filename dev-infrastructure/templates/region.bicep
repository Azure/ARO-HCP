@description('Azure Region Location')
param location string = resourceGroup().location

@description('The name of the keyvault for Maestro Eventgrid namespace certificates.')
param maestroKeyVaultName string

@description('The name of the managed identity that will manage certificates in maestros keyvault.')
param maestroKeyVaultCertOfficerMSIName string = '${maestroKeyVaultName}-cert-officer-msi'

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The maximum client sessions per authentication name for the EventGrid MQTT broker')
param maestroEventGridMaxClientSessionsPerAuthName int

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

@description('This is a global DNS zone name that will be the parent of regional DNS zones to host ARO HCP customer cluster DNS records')
param baseDNSZoneName string

@description('The resource group to deploy the base DNS zone to')
param baseDNSZoneResourceGroup string = 'global'

param regionalDNSSubdomain string

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
// M A E S T R O
//

module maestroInfra '../modules/maestro/maestro-infra.bicep' = {
  name: '${deployment().name}-maestro'
  params: {
    eventGridNamespaceName: maestroEventGridNamespacesName
    location: location
    maxClientSessionsPerAuthName: maestroEventGridMaxClientSessionsPerAuthName
    maestroKeyVaultName: maestroKeyVaultName
    kvCertOfficerManagedIdentityName: maestroKeyVaultCertOfficerMSIName
    publicNetworkAccess: 'Disabled'
  }
}
