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

@description('MSI that will be used during pipeline runs')
param globalMSIId string

@description('Enable Log Analytics')
param enableLogAnalytics bool

@description('Grafana resource ID')
param grafanaResourceId string

@description('Grafana managed identity principal ID')
param grafanaPrincipalId string

@description('Name of the Azure Monitor Workspace for services')
param svcMonitorName string

@description('Name of the Azure Monitor Workspace for hosted control planes')
param hcpMonitorName string

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

module svcMonitor '../modules/metrics/monitor.bicep' = {
  name: 'svc-monitor'
  params: {
    grafanaResourceId: grafanaResourceId
    monitorName: svcMonitorName
    purpose: 'services'
  }
}

module hcpMonitor '../modules/metrics/monitor.bicep' = {
  name: 'hcp-monitor'
  params: {
    grafanaResourceId: grafanaResourceId
    monitorName: hcpMonitorName
    purpose: 'hcps'
  }
}

// Grant Grafana permissions to query Log Analytics workspace
// This enables Grafana to visualize AFD logs and metrics from Log Analytics
module grafanaObservabilityPermissions '../modules/grafana/observability-permissions.bicep' = if (enableLogAnalytics) {
  name: 'grafana-observability-permissions'
  params: {
    grafanaPrincipalId: grafanaPrincipalId
    // AFD permissions will be granted in svc-cluster.bicep where AFD resource ID is available
    frontDoorProfileId: ''
  }
}
