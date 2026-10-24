@description('ID of the Azure Monitor Workspace for services')
param azureMonitoringWorkspaceId string

@description('ID of the Azure Monitor Workspace for hosted control planes')
param hcpAzureMonitoringWorkspaceId string

@description('ARO HCP region name')
param region string

@description('Resource ID of the SL ICM action group (empty string if not managed)')
param actionGroupSL string

@description('Resource ID of the SRE ICM action group (empty string if not managed)')
param actionGroupSRE string

@description('Resource ID of the RP ICM action group (empty string if not managed)')
param actionGroupRP string

@description('Resource ID of the MSFT ICM action group (empty string if not managed)')
param actionGroupMSFT string

@description('Resource ID of the DEV ICM action group (empty string if not managed)')
param actionGroupDEV string

@description('Whether alerting is enabled for this region')
param alertsEnabled bool

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param alertSeverityCeiling int = 0

@description('Whether to create the Event Hub action group for sending alerts to Kusto')
param eventHubAlertingEnabled bool = false

@description('Event Hub namespace name for alert events')
param alertEventsEventHubNamespaceName string = ''

@description('Event Hub name for alert events')
param alertEventsEventHubName string = ''

@description('Whether the SRE IcM action group is wired to SRE alert rules. When false, SRE rules still evaluate in Prometheus but do not deliver to IcM.')
param icmEnabledSRE bool = true

@description('Whether the SL IcM action group is wired to SL alert rules. When false, SL rules still evaluate in Prometheus but do not deliver to IcM.')
param icmEnabledSL bool = true

@description('Whether the RP IcM action group is wired to RP alert rules. When false, RP rules still evaluate in Prometheus but do not deliver to IcM.')
param icmEnabledRP bool = true

@description('Whether the MSFT IcM action group is wired to MSFT alert rules. When false, MSFT rules still evaluate in Prometheus but do not deliver to IcM.')
param icmEnabledMSFT bool = true

@description('Whether the DEV IcM action group is wired to DEV alert rules. When false, DEV rules still evaluate in Prometheus but do not deliver to IcM.')
param icmEnabledDEV bool = true

@description('Resource ID of the RP Cosmos DB account')
param rpCosmosDbAccountId string = ''

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
// ICM action groups are created at geography level and looked up via pipeline variables.
// Each lane's icmEnabled flag is a second guard so that lane's rules can evaluate without delivering IcM tickets.
// The Event Hub action group is created here (region-specific).
var slActionGroups = actionGroupSL != '' && icmEnabledSL ? concat([actionGroupSL], ehActionGroups) : ehActionGroups
var rpActionGroups = actionGroupRP != '' && icmEnabledRP ? concat([actionGroupRP], ehActionGroups) : ehActionGroups
var sreActionGroups = actionGroupSRE != '' && icmEnabledSRE ? concat([actionGroupSRE], ehActionGroups) : ehActionGroups
var msftActionGroups = actionGroupMSFT != '' && icmEnabledMSFT
  ? concat([actionGroupMSFT], ehActionGroups)
  : ehActionGroups
var devActionGroups = actionGroupDEV != '' && icmEnabledDEV ? concat([actionGroupDEV], ehActionGroups) : ehActionGroups

module serviceAlerts '../modules/metrics/service-rules.bicep' = {
  name: 'serviceAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: slActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module kustoServiceAlerts '../modules/metrics/kusto-service-rules.bicep' = {
  name: 'kustoServiceAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: ehActionGroups
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

module kustoHcpAlerts '../modules/metrics/kusto-hcp-rules.bicep' = {
  name: 'kustoHcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    actionGroups: ehActionGroups
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

module rpHcpAlerts '../modules/metrics/rp-hcp-rules.bicep' = {
  name: 'rpHcpAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
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

module devServicesAlerts '../modules/metrics/dev-services-rules.bicep' = {
  name: 'devServicesAlerts'
  params: {
    azureMonitoringWorkspaceId: azureMonitoringWorkspaceId
    actionGroups: devActionGroups
    severityCeiling: alertSeverityCeiling
  }
}

module devHcpsAlerts '../modules/metrics/dev-hcps-rules.bicep' = {
  name: 'devHcpsAlerts'
  params: {
    azureMonitoringWorkspaceId: hcpAzureMonitoringWorkspaceId
    actionGroups: devActionGroups
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
        lowEventIngestionThreshold: 100
      }
      {
        id: hcpAzureMonitoringWorkspaceId
        label: 'hcp'
        lowEventIngestionThreshold: 100
      }
    ]
  }
}

module cosmosAlerts '../modules/metrics/cosmos-alerts.bicep' = if (rpCosmosDbAccountId != '') {
  name: 'cosmosAlerts'
  params: {
    cosmosDbAccountId: rpCosmosDbAccountId
    actionGroups: slActionGroups
    enabled: alertsEnabled
    region: region
  }
}
