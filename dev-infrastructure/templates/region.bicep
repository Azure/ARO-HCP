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

@description('Set to true to prevent resources from being pruned after 48 hours')
param persist bool = false

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

param globalRegion string
param regionalRegion string

@description('The resource ID of the OCP ACR')
param ocpAcrResourceId string

@description('The resource ID of the SVC ACR')
param svcAcrResourceId string

@description('MSI that will be used during pipeline runs')
param aroDevopsMsiId string

@description('Enable Log Analytics')
param enableLogAnalytics bool

@description('Grafana resource ID')
param grafanaResourceId string

@description('Name of the Azure Monitor Workspace for services')
param svcMonitorName string

@description('Name of the Azure Monitor Workspace for hosted control planes')
param hcpMonitorName string

@description('List of emails for Dev Alerting')
param devAlertingEmails string

@description('Comma seperated list of action groups for Sev 1 alerts.')
param sev1ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 2 alerts.')
param sev2ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 3 alerts.')
param sev3ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 4 alerts.')
param sev4ActionGroupIDs string

import * as res from '../modules/resource.bicep'

// Tags the resource group
resource resourceGroupTags 'Microsoft.Resources/tags@2024-03-01' = {
  name: 'default'
  scope: resourceGroup()
  properties: {
    tags: {
      persist: toLower(string(persist))
    }
  }
}

// Reader role
// https://www.azadvertizer.net/azrolesadvertizer/acdd72a7-3385-48ef-bd42-f606fba81ae7.html
var readerRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
)

// service deployments running as the aroDevopsMsi need to lookup metadata about all kinds
// of resources, e.g. AKS metadata, database metadata, MI metadata, etc.
resource aroDevopsMSIReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(resourceGroup().id, aroDevopsMsiId, readerRoleId)
  properties: {
    principalId: reference(aroDevopsMsiId, '2023-01-31').principalId
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
// R E G I O N A L   A C R   R E P L I C A T I O N
//

var ocpAcrRef = res.acrRefFromId(ocpAcrResourceId)
var ocpAcrReplicationName = '${ocpAcrRef.name}${location}replica'
module ocpAcrReplication '../modules/acr/acr-replication.bicep' = if (globalRegion != regionalRegion) {
  name: ocpAcrReplicationName
  scope: resourceGroup(ocpAcrRef.resourceGroup.subscriptionId, ocpAcrRef.resourceGroup.name)
  params: {
    acrReplicationLocation: location
    acrReplicationParentAcrName: ocpAcrRef.name
    acrReplicationReplicaName: ocpAcrReplicationName
  }
}

var svcAcrRef = res.acrRefFromId(svcAcrResourceId)
var svcAcrReplicationName = '${svcAcrRef.name}${location}replica'
module svcAcrReplication '../modules/acr/acr-replication.bicep' = if (globalRegion != regionalRegion) {
  name: svcAcrReplicationName
  scope: resourceGroup(svcAcrRef.resourceGroup.subscriptionId, svcAcrRef.resourceGroup.name)
  params: {
    acrReplicationLocation: location
    acrReplicationParentAcrName: svcAcrRef.name
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
    certificateIssuer: maestroCertificateIssuer
    logAnalyticsWorkspaceId: enableLogAnalytics ? logAnalyticsWorkspace.id : ''
  }
}

//
//   L O G   A N A L Y T I C S
//

resource logAnalyticsWorkspace 'Microsoft.OperationalInsights/workspaces@2023-09-01' = if (enableLogAnalytics) {
  name: 'log-analytics-workspace'
  location: resourceGroup().location
  properties: {
    sku: {
      name: 'PerGB2018'
    }
    retentionInDays: 90
  }
}

//
//   M O N I T O R I N G
//

module svcMonitoring '../modules/metrics/monitor.bicep' = {
  name: 'svc-monitor'
  params: {
    grafanaResourceId: grafanaResourceId
    monitorName: svcMonitorName
  }
}

module hcpMonitoring '../modules/metrics/monitor.bicep' = {
  name: 'hcp-monitor'
  params: {
    grafanaResourceId: grafanaResourceId
    monitorName: hcpMonitorName
  }
}

module actionGroups '../modules/metrics/actiongroups.bicep' = {
  name: 'actionGroups'
  params: {
    devAlertingEmails: devAlertingEmails
    sev1ActionGroupIDs: sev1ActionGroupIDs
    sev2ActionGroupIDs: sev2ActionGroupIDs
    sev3ActionGroupIDs: sev3ActionGroupIDs
    sev4ActionGroupIDs: sev4ActionGroupIDs
  }
}

module serviceAlerts '../modules/metrics/service-rules.bicep' = {
  name: 'serviceAlerts'
  params: {
    azureMonitoringWorkspaceId: svcMonitoring.outputs.monitorId
    allSev1ActionGroups: actionGroups.outputs.allSev1ActionGroups
    allSev2ActionGroups: actionGroups.outputs.allSev2ActionGroups
    allSev3ActionGroups: actionGroups.outputs.allSev3ActionGroups
    allSev4ActionGroups: actionGroups.outputs.allSev4ActionGroups
  }
}
