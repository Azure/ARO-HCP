// Shared alerting infrastructure for the opstool environment
// This creates a shared email action group that can be used by all apps in the cluster

@description('Email address for alert notifications')
param alertEmail string

@description('Enable or disable alerting')
param alertingEnabled bool = true

// Shared Email Action Group for all opstool alerts
resource emailActionGroup 'Microsoft.Insights/actionGroups@2024-10-01-preview' = {
  name: 'opstool-email-alerts'
  location: 'global'
  properties: {
    enabled: alertingEnabled
    groupShortName: 'opstool-ag'
    emailReceivers: [
      {
        name: 'primary-contact'
        emailAddress: alertEmail
        useCommonAlertSchema: true
      }
    ]
  }
}

output actionGroupId string = emailActionGroup.id
output actionGroupName string = emailActionGroup.name
