// Per-environment DEV CI quota monitoring dashboards.
// Replaces the manually-authored dashboard (resource group "dashboards",
// name 901b128a-124f-43e6-a797-5fcf3d1e83fe) with fully IaC dashboards, one
// per environment, each scoped to that environment's subscriptions/regions.
// The old manually-authored dashboard has been manually deleted from Azure;
// ARM does not track/manage it (it predates this template), so its removal
// isn't part of this deployment.
// All four dashboards query the same DEV-only Azure Monitor Workspace: the
// tenant-quota-collector runs once, centrally, in DEV, and remotely collects
// quota for every environment's subscriptions.

@description('Azure Monitor Workspace resource ID queried by every panel')
param azureMonitorWorkspaceId string

@description('tenant_name label value for the Directory (Azure AD object) quota panels')
param tenantName string

@description('Dev environment subscriptions charted (infrastructure + E2E subscriptions)')
param devSubscriptions array

@description('Dev environment regions charted for compute/network quota')
param devRegions array

@description('Int environment subscriptions charted')
param intSubscriptions array

@description('Int environment regions charted for compute/network quota')
param intRegions array

@description('Stg environment subscriptions charted')
param stgSubscriptions array

@description('Stg environment regions charted for compute/network quota')
param stgRegions array

@description('Prod environment subscriptions charted')
param prodSubscriptions array

@description('Prod environment regions charted for compute/network quota')
param prodRegions array

module devDashboard '../../dev-infrastructure/modules/monitor/tenant-quota-dashboard.bicep' = {
  name: 'dev-ci-quota-dashboard-dev'
  params: {
    dashboardName: 'arohcpdevci-quota-dev'
    location: resourceGroup().location
    envLabel: 'Dev'
    azureMonitorWorkspaceId: azureMonitorWorkspaceId
    subscriptions: devSubscriptions
    regions: devRegions
    tenantName: tenantName
  }
}

module intDashboard '../../dev-infrastructure/modules/monitor/tenant-quota-dashboard.bicep' = {
  name: 'dev-ci-quota-dashboard-int'
  params: {
    dashboardName: 'arohcpdevci-quota-int'
    location: resourceGroup().location
    envLabel: 'Int'
    azureMonitorWorkspaceId: azureMonitorWorkspaceId
    subscriptions: intSubscriptions
    regions: intRegions
    tenantName: tenantName
  }
}

module stgDashboard '../../dev-infrastructure/modules/monitor/tenant-quota-dashboard.bicep' = {
  name: 'dev-ci-quota-dashboard-stg'
  params: {
    dashboardName: 'arohcpdevci-quota-stg'
    location: resourceGroup().location
    envLabel: 'Stg'
    azureMonitorWorkspaceId: azureMonitorWorkspaceId
    subscriptions: stgSubscriptions
    regions: stgRegions
    tenantName: tenantName
  }
}

module prodDashboard '../../dev-infrastructure/modules/monitor/tenant-quota-dashboard.bicep' = {
  name: 'dev-ci-quota-dashboard-prod'
  params: {
    dashboardName: 'arohcpdevci-quota-prod'
    location: resourceGroup().location
    envLabel: 'Prod'
    azureMonitorWorkspaceId: azureMonitorWorkspaceId
    subscriptions: prodSubscriptions
    regions: prodRegions
    tenantName: tenantName
  }
}
