@description('ID of the Azure Monitor Workspace for services')
param azureMonitoringWorkspaceId string

@description('ID of the Azure Monitor Workspace for hosted control planes')
param hcpAzureMonitoringWorkspaceId string

@description('ARO HCP region name')
param region string

@description('The ICM environment')
param icmEnvironment string

@description('ICM connection Name')
param icmConnectionName string

@description('ICM connection id')
param icmConnectionId string

@description('Name of the ICM Action Group')
param icmActionGroupNameSRE string

@description('Name of the ICM Action Group')
param icmActionGroupShortNameSRE string

@description('ICM routing ID')
param icmRoutingIdSRE string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSRE string

@description('Name of the ICM Action Group')
param icmActionGroupNameSL string

@description('Name of the ICM Action Group')
param icmActionGroupShortNameSL string

@description('ICM routing ID')
param icmRoutingIdSL string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSL string

@description('Name of the ICM Action Group')
param icmActionGroupNameRP string

@description('Name of the ICM Action Group')
param icmActionGroupShortNameRP string

@description('ICM routing ID')
param icmRoutingIdRP string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledRP string

@description('Name of the ICM Action Group')
param icmActionGroupNameMSFT string

@description('Name of the ICM Action Group')
param icmActionGroupShortNameMSFT string

@description('ICM routing ID')
param icmRoutingIdMSFT string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledMSFT string

@description('Enable creating ICM action groups')
param manageConnection bool

@description('Whether ICM alerting is enabled for this region')
param alertsEnabled bool

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param alertSeverityCeiling int = 0

@description('Whether to create the Event Hub action group for sending alerts to Kusto')
param eventHubAlertingEnabled bool = false

@description('Event Hub namespace name for alert events')
param alertEventsEventHubNamespaceName string = ''

@description('Event Hub name for alert events')
param alertEventsEventHubName string = ''

module actionGroups '../modules/metrics/actiongroups.bicep' = if (manageConnection) {
  name: 'actionGroups'
  params: {
    icmEnvironment: icmEnvironment
    icmConnectionName: icmConnectionName
    icmConnectionId: icmConnectionId
    icmActionGroupNameSRE: icmActionGroupNameSRE
    icmActionGroupShortNameSRE: icmActionGroupShortNameSRE
    icmRoutingIdSRE: icmRoutingIdSRE
    icmAutomitigationEnabledSRE: icmAutomitigationEnabledSRE
    icmActionGroupNameSL: icmActionGroupNameSL
    icmActionGroupShortNameSL: icmActionGroupShortNameSL
    icmRoutingIdSL: icmRoutingIdSL
    icmAutomitigationEnabledSL: icmAutomitigationEnabledSL
    icmActionGroupNameRP: icmActionGroupNameRP
    icmActionGroupShortNameRP: icmActionGroupShortNameRP
    icmRoutingIdRP: icmRoutingIdRP
    icmAutomitigationEnabledRP: icmAutomitigationEnabledRP
    icmActionGroupNameMSFT: icmActionGroupNameMSFT
    icmActionGroupShortNameMSFT: icmActionGroupShortNameMSFT
    icmRoutingIdMSFT: icmRoutingIdMSFT
    icmAutomitigationEnabledMSFT: icmAutomitigationEnabledMSFT
    alertingEnabled: alertsEnabled
  }
}

module eventHubActionGroup '../modules/metrics/eventhub-actiongroup.bicep' = if (eventHubAlertingEnabled) {
  name: 'eventHubActionGroup'
  params: {
    alertingEnabled: alertsEnabled
    alertEventsEventHubNamespaceName: alertEventsEventHubNamespaceName
    alertEventsEventHubName: alertEventsEventHubName
  }
}

var ehActionGroups = eventHubAlertingEnabled ? [eventHubActionGroup!.outputs.actionGroupId] : []

// Action group arrays per IcM team, combined with the Event Hub action group.
// Each alert module receives one of these arrays, determining where its alerts route:
//   - To IcM + Kusto: use one of the team-specific arrays (e.g., slActionGroups)
//   - To Kusto only (no IcM): use ehActionGroups directly
// This choice is made per module call below — one call per generated Bicep file.
var slActionGroups = manageConnection ? concat([actionGroups!.outputs.actionGroupsSL], ehActionGroups) : ehActionGroups
var rpActionGroups = manageConnection ? concat([actionGroups!.outputs.actionGroupsRP], ehActionGroups) : ehActionGroups
var sreActionGroups = manageConnection
  ? concat([actionGroups!.outputs.actionGroupsSRE], ehActionGroups)
  : ehActionGroups
var msftActionGroups = manageConnection
  ? concat([actionGroups!.outputs.actionGroupsMSFT], ehActionGroups)
  : ehActionGroups

module serviceAlerts '../modules/metrics/service-rules.bicep' = {
  name: 'serviceAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: slActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module svcKubeAlerts '../modules/metrics/svc-kube-rules.bicep' = {
  name: 'svcKubeAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: slActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module hcpAlerts '../modules/metrics/hcp-rules.bicep' = {
  name: 'hcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    actionGroups: sreActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module slHcpAlerts '../modules/metrics/sl-hcp-rules.bicep' = {
  name: 'slHcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    actionGroups: slActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module sreServiceAlerts '../modules/metrics/sre-service-rules.bicep' = {
  name: 'sreServiceAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: sreActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module rpAlerts '../modules/metrics/rp-rules.bicep' = {
  name: 'rpAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: rpActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module msftAlerts '../modules/metrics/msft-rules.bicep' = {
  name: 'msftAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: msftActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module ingestionAlerts '../modules/metrics/amw-ingestion-alerts.bicep' = {
  name: 'ingestionAlerts'
  params: {
    actionGroups: slActionGroups
    enabled: alertsEnabled
    region: region
    workspaces: [
      {
        id: azureMonitoringWorkspaceId
        label: 'svc'
        lowEventIngestionThreshold: 1
      }
      {
        id: hcpAzureMonitoringWorkspaceId
        label: 'hcp'
        lowEventIngestionThreshold: 5
      }
    ]
  }
}

output actionGroupSL string = manageConnection ? actionGroups!.outputs.actionGroupsSL : ''
output actionGroupAlertEH string = eventHubAlertingEnabled ? eventHubActionGroup!.outputs.actionGroupId : ''
