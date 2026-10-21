@description('PagerDuty Microsoft Azure integration URL.')
@secure()
param integrationUrl string

@description('Enable or disable alerting.')
param alertingEnabled bool

resource pagerDutyActionGroup 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'opstool-pagerduty'
  location: 'global'
  properties: {
    enabled: alertingEnabled
    groupShortName: 'opstool-pd'
    webhookReceivers: [
      {
        name: 'pagerduty'
        serviceUri: integrationUrl
        useCommonAlertSchema: false
      }
    ]
  }
}

output actionGroupId string = pagerDutyActionGroup.id
output actionGroupName string = pagerDutyActionGroup.name
