// RP-owned alerts and independently controlled cluster-upgrade detectors.

@description('The Azure resource ID of the Azure Monitor Workspace (stores prometheus metrics for services/aks level metrics)')
param azureMonitoringWorkspaceId string

param actionGroups array

@description('The minimum IcM severity level (highest priority) that alerts can fire at. Alerts more critical than this ceiling will be degraded to this value. 0 means no ceiling.')
param severityCeiling int = 0

@allowed([
  'legacy'
  'shadow'
  'active'
])
@description('Cluster-upgrade rollout mode. Legacy preserves current paging; shadow evaluates without delivery; active enables the new production detector.')
param clusterUpgradeMonitorMode string = 'legacy'

module generatedAlerts 'rules/generatedRPPrometheusAlertingRules.bicep' = {
  name: 'generatedRPPrometheusAlertingRules'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
    severityCeiling: severityCeiling
  }
}

module legacyUpgradeAlerts 'rules/generatedRPUpgradeLegacyAlertingRules.bicep' = {
  name: 'legacyClusterUpgradeAlerts'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: actionGroups
    severityCeiling: severityCeiling
    alertRulesEnabled: clusterUpgradeMonitorMode != 'active'
  }
}

module shadowUpgradeAlerts 'rules/generatedRPUpgradeShadowAlertingRules.bicep' = {
  name: 'shadowClusterUpgradeAlerts'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: []
    severityCeiling: severityCeiling
    alertRulesEnabled: clusterUpgradeMonitorMode == 'shadow'
  }
}

module upgradeAlerts 'rules/generatedRPUpgradeAlertingRules.bicep' = {
  name: 'clusterUpgradeAlerts'
  params: {
    azureMonitoring: azureMonitoringWorkspaceId
    actionGroups: clusterUpgradeMonitorMode == 'active' ? actionGroups : []
    severityCeiling: severityCeiling
    alertRulesEnabled: clusterUpgradeMonitorMode == 'active'
  }
}
