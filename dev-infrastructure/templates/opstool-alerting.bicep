// Shared alerting infrastructure for the opstool environment

@description('Name of the opstool workload Key Vault')
param workloadKVName string

@description('Enable or disable alerting')
param alertingEnabled bool = true

resource workloadKV 'Microsoft.KeyVault/vaults@2024-04-01-preview' existing = {
  name: workloadKVName
}

module pagerDutyActionGroup '../modules/metrics/pagerduty-actiongroup.bicep' = {
  name: 'opstool-pagerduty-action-group'
  params: {
    alertingEnabled: alertingEnabled
    integrationUrl: workloadKV.getSecret('pagerduty-azure-integration-url')
  }
}

output actionGroupId string = pagerDutyActionGroup.outputs.actionGroupId
output actionGroupName string = pagerDutyActionGroup.outputs.actionGroupName
