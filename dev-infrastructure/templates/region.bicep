@description('Azure Region Location')
param location string = resourceGroup().location

@description('The name of the eventgrid namespace for Maestro.')
param maestroEventGridNamespacesName string

@description('The maximum client sessions per authentication name for the EventGrid MQTT broker')
param maestroEventGridMaxClientSessionsPerAuthName int

@description('Allow/deny public network access to the Maestro EventGrid Namespace')
param maestroEventGridPrivate bool

@description('The certificate issuer for the EventGrid Namespace')
param maestroCertificateIssuer string

@description('''
  This is the global parent DNS zone for ARO HCP customer cluster DNS.
  It is prefixed with regionalDNSSubdomain to form the actual regional DNS zone name
  ''')
param cxParentZoneResourceId string

@description('''
  This is the global parent DNS zone for ARO HCP service DNS records.
  It is prefixed with regionalDNSSubdomain to form the actual regional DNS zone name
  ''')
param svcParentZoneResourceId string

param regionalDNSSubdomain string

@description('The name of the Cosmos DB for the RP')
param rpCosmosDbName string

@description('If true, make the Cosmos DB instance private')
param rpCosmosDbPrivate bool

@description('The zone redundant mode of the Cosmos DB instance')
param rpCosmosZoneRedundantMode string

@description('Enables Cosmos DB burst capacity for the RP CosmosDB account')
param rpCosmosEnableBurstCapacity bool = false

@description('disableLocalAuth for the ARO HCP RP CosmosDB')
param disableLocalAuth bool

@description('MSI that will be used during pipeline runs')
param globalMSIId string

@description('Grafana resource ID')
param grafanaResourceId string

@description('Name of the Azure Monitor Workspace for services')
param svcMonitorName string

@description('Name of the Azure Monitor Workspace for hosted control planes')
param hcpMonitorName string

@description('Existing services Azure Monitor Workspace resource ID, or empty to create an owned workspace')
param svcWorkspaceResourceId string = ''

@description('Existing HCP Azure Monitor Workspace resource ID, or empty to create an owned workspace')
param hcpWorkspaceResourceId string = ''

import { determineZoneRedundancyForRegion } from '../modules/common.bicep'
import * as res from '../modules/resource.bicep'

// Reader role
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

// service deployments running as the aroDevopsMsi need to lookup metadata about all kinds
// of resources, e.g. AKS metadata, database metadata, MI metadata, etc.
resource aroDevopsMSIReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, globalMSIId, readerRoleId)
  properties: {
    principalId: reference(globalMSIId, '2023-01-31').principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: readerRoleId
  }
}

//
// R E G I O N A L   C X   D N S   Z O N E
//

var cxParentZoneRef = res.dnsZoneRefFromId(cxParentZoneResourceId)

resource regionalCxZone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: '${regionalDNSSubdomain}.${cxParentZoneRef.name}'
  location: 'global'
}

module regionalCxZoneDelegation '../modules/dns/zone-delegation.bicep' = {
  name: '${regionalDNSSubdomain}-cx-zone-deleg'
  scope: resourceGroup(cxParentZoneRef.resourceGroup.subscriptionId, cxParentZoneRef.resourceGroup.name)
  params: {
    childZoneName: regionalDNSSubdomain
    childZoneNameservers: regionalCxZone.properties.nameServers
    parentZoneName: cxParentZoneRef.name
  }
}

//
// R E G I O N A L   S V C   D N S   Z O N E
//

var svcParentZoneRef = res.dnsZoneRefFromId(svcParentZoneResourceId)

resource regionalSvcZone 'Microsoft.Network/dnsZones@2018-05-01' = {
  name: '${regionalDNSSubdomain}.${svcParentZoneRef.name}'
  location: 'global'
}

module regionalSvcZoneDelegation '../modules/dns/zone-delegation.bicep' = {
  name: '${regionalDNSSubdomain}-svc-zone-deleg'
  scope: resourceGroup(svcParentZoneRef.resourceGroup.subscriptionId, svcParentZoneRef.resourceGroup.name)
  params: {
    childZoneName: regionalDNSSubdomain
    childZoneNameservers: regionalSvcZone.properties.nameServers
    parentZoneName: svcParentZoneRef.name
  }
}

//
// M A E S T R O
//

module maestroInfra '../modules/maestro/maestro-infra.bicep' = {
  name: 'maestro-infra-deployment'
  params: {
    eventGridNamespaceName: maestroEventGridNamespacesName
    location: location
    maxClientSessionsPerAuthName: maestroEventGridMaxClientSessionsPerAuthName
    publicNetworkAccess: maestroEventGridPrivate ? 'Disabled' : 'Enabled'
    certificateIssuer: maestroCertificateIssuer
  }
}

//
//   C O S M O S D B
//

module rpCosmosAccount '../modules/rp-cosmos-account.bicep' = {
  name: 'rp-cosmos-account'
  params: {
    name: rpCosmosDbName
    location: location
    zoneRedundant: determineZoneRedundancyForRegion(location, rpCosmosZoneRedundantMode)
    disableLocalAuth: disableLocalAuth
    private: rpCosmosDbPrivate
    enableBurstCapacity: rpCosmosEnableBurstCapacity
  }
}

//
//   M O N I T O R I N G
//

module svcMonitor '../modules/metrics/monitor.bicep' = if (svcWorkspaceResourceId == '') {
  name: 'svc-monitor'
  params: {
    grafanaResourceId: grafanaResourceId
    monitorName: svcMonitorName
    purpose: 'services'
  }
}

module hcpMonitor '../modules/metrics/monitor.bicep' = if (hcpWorkspaceResourceId == '') {
  name: 'hcp-monitor'
  params: {
    grafanaResourceId: grafanaResourceId
    monitorName: hcpMonitorName
    purpose: 'hcps'
  }
}

// External workspaces skip monitor.bicep, but still need Grafana query access.
// Match monitor.bicep's compiled resource ID casing to preserve its persisted assignment GUIDs.
var localSvcWorkspaceId = resourceId('Microsoft.Monitor/accounts', svcMonitorName)
var localHcpWorkspaceId = resourceId('Microsoft.Monitor/accounts', hcpMonitorName)
var ownedWorkspaceIds = union(
  empty(svcWorkspaceResourceId) ? [toLower(localSvcWorkspaceId)] : [],
  empty(hcpWorkspaceResourceId) ? [toLower(localHcpWorkspaceId)] : []
)
// Do not duplicate grants already deployed by either owned monitor module.
var externalWorkspaceIds = empty(grafanaResourceId) ? [] : filter(
  union(
    empty(svcWorkspaceResourceId) ? [] : [toLower(svcWorkspaceResourceId)],
    empty(hcpWorkspaceResourceId) ? [] : [toLower(hcpWorkspaceResourceId)]
  ),
  id => !contains(ownedWorkspaceIds, id)
)
var externalWorkspaceRefs = map(externalWorkspaceIds, id => res.monitoringWorkspaceRefFromId(id))
var grafanaRef = res.grafanaRefFromId(grafanaResourceId)

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' existing = if (!empty(externalWorkspaceIds)) {
  name: grafanaRef.name
  scope: resourceGroup(grafanaRef.resourceGroup.subscriptionId, grafanaRef.resourceGroup.name)
}

module externalMonitorGrafanaRoles '../modules/metrics/amw-role-assignment.bicep' = [for (workspace, i) in externalWorkspaceRefs: {
  name: 'grafana-amw-${uniqueString(grafanaResourceId, workspace.resourceGroup.subscriptionId, workspace.resourceGroup.name, workspace.name)}'
  scope: resourceGroup(workspace.resourceGroup.subscriptionId, workspace.resourceGroup.name)
  params: {
    workspaceName: workspace.name
    principalId: grafana!.identity.principalId
    roleDefinitionId: 'b0d8363b-8ddd-447d-831f-62ca05bff136'
    roleAssignmentName: externalWorkspaceIds[i] == toLower(localSvcWorkspaceId)
      ? guid(localSvcWorkspaceId, grafana!.id, 'b0d8363b-8ddd-447d-831f-62ca05bff136')
      : externalWorkspaceIds[i] == toLower(localHcpWorkspaceId)
          ? guid(localHcpWorkspaceId, grafana!.id, 'b0d8363b-8ddd-447d-831f-62ca05bff136')
          : ''
  }
}]

// Ingestion limits for Azure Monitor Workspaces are managed dynamically by the
// AMW scaling controller in the fleet component (fleet/pkg/controllers/amwscaling).
// Do NOT set metricsContainers limits here — a region redeploy would overwrite
// controller-driven increases back to static config values.
