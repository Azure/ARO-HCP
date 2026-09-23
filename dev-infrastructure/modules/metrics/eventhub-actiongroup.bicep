@description('Indicates if alerting should be enabled for this region. When true, action groups will be enabled.')
param alertingEnabled bool

@description('Event Hub namespace name for alert events')
param alertEventsEventHubNamespaceName string

@description('Event Hub name for alert events')
param alertEventsEventHubName string

@description('Subscription ID where the Event Hub namespace resides. Defaults to the current deployment subscription.')
param eventHubSubscriptionId string = subscription().subscriptionId

resource alertEventsEH 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'alert-eventhub-action-group'
  location: 'global'
  properties: {
    enabled: alertingEnabled
    groupShortName: 'alertEH'
    eventHubReceivers: [
      {
        name: 'alertEventsEventHub'
        eventHubNameSpace: alertEventsEventHubNamespaceName
        eventHubName: alertEventsEventHubName
        subscriptionId: eventHubSubscriptionId
        tenantId: tenant().tenantId
        useCommonAlertSchema: true
      }
    ]
  }
}

output actionGroupId string = alertEventsEH.id
