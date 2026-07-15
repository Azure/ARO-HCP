@description('The ICM environment')
param icmEnvironment string

@description('ICM connection Name')
param icmConnectionName string

@description('ICM connection id')
param icmConnectionId string

@description('ICM incident receiver name for SRE')
param icmActionGroupNameSRE string

@description('Short display name for the SRE action group')
param icmActionGroupShortNameSRE string

@description('ICM routing ID')
param icmRoutingIdSRE string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSRE string

@description('ICM incident receiver name for SL')
param icmActionGroupNameSL string

@description('Short display name for the SL action group')
param icmActionGroupShortNameSL string

@description('ICM routing ID')
param icmRoutingIdSL string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledSL string

@description('ICM incident receiver name for RP')
param icmActionGroupNameRP string

@description('Short display name for the RP action group')
param icmActionGroupShortNameRP string

@description('ICM routing ID')
param icmRoutingIdRP string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledRP string

@description('ICM incident receiver name for MSFT')
param icmActionGroupNameMSFT string

@description('Short display name for the MSFT action group')
param icmActionGroupShortNameMSFT string

@description('ICM routing ID')
param icmRoutingIdMSFT string

@description('ICM automitigation enabled ID')
param icmAutomitigationEnabledMSFT string

@description('Enable creating ICM action groups')
param manageConnection bool

@description('Whether alerting is enabled')
param alertsEnabled bool

@description('Whether to create the Event Hub action group for sending alerts to Kusto')
param eventHubAlertingEnabled bool = false

@description('Event Hub namespace name for alert events')
param alertEventsEventHubNamespaceName string = ''

@description('Event Hub name for alert events')
param alertEventsEventHubName string = ''

@description('Subscription ID where the Event Hub namespace resides')
param eventHubSubscriptionId string = subscription().subscriptionId

@description('Region of the Kusto cluster (from kusto-lookup output)')
param kustoRegion string = ''

@description('Region of this deployment')
param regionLocation string

module eventHubActionGroup '../modules/metrics/eventhub-actiongroup.bicep' = if (eventHubAlertingEnabled && kustoRegion == regionLocation) {
  name: 'eventHubActionGroup'
  params: {
    alertingEnabled: alertsEnabled
    alertEventsEventHubNamespaceName: alertEventsEventHubNamespaceName
    alertEventsEventHubName: alertEventsEventHubName
    eventHubSubscriptionId: eventHubSubscriptionId
  }
}

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

output actionGroupSL string = manageConnection ? actionGroups!.outputs.actionGroupsSL : ''
output actionGroupSRE string = manageConnection ? actionGroups!.outputs.actionGroupsSRE : ''
output actionGroupRP string = manageConnection ? actionGroups!.outputs.actionGroupsRP : ''
output actionGroupMSFT string = manageConnection ? actionGroups!.outputs.actionGroupsMSFT : ''
output actionGroupAlertEH string = eventHubAlertingEnabled && kustoRegion == regionLocation
  ? eventHubActionGroup!.outputs.actionGroupId
  : ''
