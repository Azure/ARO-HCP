import { getLocationAvailabilityZonesCSV, determineZoneRedundancy, csvToArray } from '../modules/common.bicep'

@description('Azure Global Location')
param location string

@description('The global msi name')
param globalMSIName string

@description('Name of the keyvault where the pull secret is stored')
param keyVaultName string

@description('Resource group of the keyvault')
param keyVaultPrivate bool

@description('Defines if the keyvault should have soft delete enabled')
param keyVaultSoftDelete bool

@description('Tag key for the keyvault')
param keyVaultTagKey string

@description('Tag value for the keyvault')
param keyVaultTagValue string

@description('The cxParentZone Domain')
param cxParentZoneName string

@description('The svcParentZone Domain')
param svcParentZoneName string

@description('Domain Team MSI to delegate child DNS')
param safeDnsIntAppObjectId string

@description('Global Grafana instance name')
param grafanaName string

@description('The Grafana major version')
param grafanaMajorVersion string

@description('List of grafana role assignments as a space-separated list of items in the format of "principalId/principalType/role"')
param grafanaRoles string

@description('The zone redundant mode of Grafana')
param grafanaZoneRedundantMode string

@description('Availability Zones to use for the infrastructure, as a CSV string. Defaults to all the zones of the location')
param locationAvailabilityZones string = getLocationAvailabilityZonesCSV(location)
var locationAvailabilityZoneList = csvToArray(locationAvailabilityZones)

@description('Tha name of the SVC NSP')
param globalNSPName string

@description('Access mode for this NSP')
param globalNSPAccessMode string

param oidcSubdomain string
param azureFrontDoorProfileName string
param azureFrontDoorKeyVaultName string
param azureFrontDoorKeyVaultTagKey string
param azureFrontDoorKeyVaultTagValue string
param azureFrontDoorUseManagedCertificates bool
param azureFrontDoorSkuName string
param keyVaultAdminPrincipalId string
param oidcMsiName string

//
//  G L O B A L   M S I
//

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: globalMSIName
  location: location
}

//   G L O B A L    K V

module globalKV '../modules/keyvault/keyvault.bicep' = {
  name: 'global-kv'
  params: {
    location: location
    keyVaultName: keyVaultName
    private: keyVaultPrivate
    enableSoftDelete: keyVaultSoftDelete
    tagKey: keyVaultTagKey
    tagValue: keyVaultTagValue
  }
}

module globalMSIKVSecretUser '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(globalMSI.id, globalKV.name, 'secret-officer')
  params: {
    keyVaultName: keyVaultName
    roleName: 'Key Vault Secrets Officer'
    managedIdentityPrincipalId: globalMSI.properties.principalId
  }
}

module globalMSIKVCryptoUser '../modules/keyvault/keyvault-secret-access.bicep' = {
  name: guid(globalMSI.id, globalKV.name, 'crypto-officer')
  params: {
    keyVaultName: keyVaultName
    roleName: 'Key Vault Crypto Officer'
    managedIdentityPrincipalId: globalMSI.properties.principalId
  }
}

module encryptionKey '../modules/keyvault/key-vault-key.bicep' = {
  name: 'imagesync-secretSyncKey'
  params: {
    keyVaultName: globalKV.outputs.kvName
    keyName: 'secretSyncKey'
  }
}

//
//   R E A D E R   R O L E S
//
// service deployments running as the aroDevopsMsi need to lookup metadata about all kinds
// of resources, e.g. AKS metadata, database metadata, MI metadata, etc.

// Reader role
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

resource aroDevopsMSIReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, globalMSI.id, readerRoleId)
  properties: {
    principalId: globalMSI.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
//  C X   P A R E N T   Z O N E
//

resource cxParentZone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: cxParentZoneName
  location: 'global'
}

resource caaRecord 'Microsoft.Network/dnsZones/CAA@2023-07-01-preview' = {
  name: '@'
  parent: cxParentZone
  properties: {
    TTL: 3600
    caaRecords: [
      {
        flags: 0
        tag: 'issue'
        value: 'digicert.com'
      }
      {
        flags: 0
        tag: 'issue'
        value: 'microsoft.com'
      }
      {
        flags: 0
        tag: 'iodef'
        value: 'mailto:caarecordaware@microsoft.com'
      }
    ]
  }
}

resource spfRecord 'Microsoft.Network/dnsZones/TXT@2023-07-01-preview' = {
  name: '@'
  parent: cxParentZone
  properties: {
    TTL: 3600
    TXTRecords: [
      {
        value: [
          'v=spf1 -all'
        ]
      }
    ]
  }
}

resource dkimRecord 'Microsoft.Network/dnsZones/TXT@2023-07-01-preview' = {
  name: '*._domainkey'
  parent: cxParentZone
  properties: {
    TTL: 3600
    TXTRecords: [
      {
        value: [
          'v=DKIM1; p='
        ]
      }
    ]
  }
}

resource dmarcRecord 'Microsoft.Network/dnsZones/TXT@2023-07-01-preview' = {
  name: '_dmarc'
  parent: cxParentZone
  properties: {
    TTL: 3600
    TXTRecords: [
      {
        value: [
          'v=DMARC1; p=reject; pct=100; rua=mailto:rua@dmarc.microsoft; ruf=mailto:ruf@dmarc.microsoft; fo=1'
        ]
      }
    ]
  }
}

//
//  S V C    P A R E N T   Z O N E
//

resource svcParentZone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: svcParentZoneName
  location: 'global'
}

// DNS Zone Contributor: Lets SafeDnsIntApplication manage DNS zones and record sets in Azure DNS, but does not let it control who has access to them.
// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/networking#dns-zone-contributor
var dnsZoneContributor = 'befefa01-2a29-4197-83a8-272ff33ce314'

resource cxParentZoneRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(safeDnsIntAppObjectId)) {
  name: guid(cxParentZone.id, safeDnsIntAppObjectId, dnsZoneContributor)
  scope: cxParentZone
  properties: {
    principalId: safeDnsIntAppObjectId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions/', dnsZoneContributor)
  }
}

resource svcParentZoneRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(safeDnsIntAppObjectId)) {
  name: guid(svcParentZone.id, safeDnsIntAppObjectId, dnsZoneContributor)
  scope: svcParentZone
  properties: {
    principalId: safeDnsIntAppObjectId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions/', dnsZoneContributor)
  }
}

//
//   G R A F A N A
//

// why do we read the registered workspace IDs and feed it into grafana creation again?
// becaue the grafana ARM resource expects the list to be provided or otherwise wipes the
// existing integrations. This is a workaround to keep the existing integrations while
// still being able to reconcile the grafana resource itself.
// we might want to have another source of truth for the integrations in the future, e.g.
// some sort of inventory of the regions of ARO HCP

module grafanaWorkspaceIdLookup '../modules/grafana/integration-lookup.bicep' = {
  name: 'grafana-workspace-lookup'
  params: {
    location: location
    grafanaName: grafanaName
    deploymentScriptIdentityId: globalMSI.id
  }
}

module grafana '../modules/grafana/instance.bicep' = {
  name: 'grafana'
  params: {
    location: location
    grafanaName: grafanaName
    grafanaMajorVersion: grafanaMajorVersion
    grafanaManagerPrincipalId: globalMSI.properties.principalId
    grafanaRoles: grafanaRoles
    zoneRedundancy: determineZoneRedundancy(locationAvailabilityZoneList, grafanaZoneRedundantMode)
    azureMonitorWorkspaceIds: grafanaWorkspaceIdLookup.outputs.azureMonitorWorkspaceIds
  }
}

//
//   N E T W O R K    S E C U R I T Y    P E R I M E T E R
//

module globalNSP '../modules/network/nsp.bicep' = {
  name: 'nsp-${uniqueString(resourceGroup().name)}'
  params: {
    nspName: globalNSPName
    location: location
  }
}

module globalNSPProfile '../modules/network/nsp-profile.bicep' = {
  name: 'profile-${uniqueString(resourceGroup().name)}'
  params: {
    accessMode: globalNSPAccessMode
    nspName: globalNSPName
    profileName: globalNSPName
    location: location
    associatedResources: [
      globalKV.outputs.kvId
    ]
    subscriptions: [
      //TODO: add ev2 access
      subscription().id
    ]
  }
}

//
//   A Z U R E   F R O N T   D O O R
//

module azureFrontDoor '../modules/oidc/global/main.bicep' = {
  name: 'azureFrontDoor'
  params: {
    subdomain: oidcSubdomain
    parentZoneName: svcParentZoneName
    frontDoorProfileName: azureFrontDoorProfileName
    frontDoorEndpointName: azureFrontDoorProfileName
    frontDoorSkuName: azureFrontDoorSkuName
    securityPolicyName: azureFrontDoorProfileName
    wafPolicyName: azureFrontDoorProfileName
    keyVaultName: azureFrontDoorKeyVaultName
    keyVaultTagKey: azureFrontDoorKeyVaultTagKey
    keyVaultTagValue: azureFrontDoorKeyVaultTagValue
    useManagedCertificates: azureFrontDoorUseManagedCertificates
    keyVaultAdminSPObjId: keyVaultAdminPrincipalId
    oidcMsiName: oidcMsiName
  }
}

output globalKeyVaultUrl string = globalKV.outputs.kvUrl
