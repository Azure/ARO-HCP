/*
Sets up the global ACRs for SVC and OCP images.
*/
import { determineZoneRedundancyForRegion } from '../modules/common.bicep'

param ocpAcrName string
param ocpAcrSku string
param ocpAcrUntaggedImagesRetentionEnabled bool
param ocpAcrUntaggedImagesRetentionDays int

param svcAcrName string
param svcAcrSku string
param svcAcrUntaggedImagesRetentionEnabled bool
param svcAcrUntaggedImagesRetentionDays int

param globalMSIName string

param globalKeyVaultName string

param location string

@description('The zone redundancy mode for the OCP ACR')
param ocpAcrZoneRedundantMode string

@description('The zone redundancy mode for the SVC ACR')
param svcAcrZoneRedundantMode string

@description('Deploy mise artifact sync, only valid in Microsoft Production and AME Tenants')
param deployMiseArtifactSync bool = false

@description('Enable diagnostic settings (repository/login events + metrics) for the OCP ACR')
param ocpAcrDiagnosticSettingsEnabled bool = false

@description('Name of the Log Analytics workspace to create for OCP ACR diagnostic logs')
param ocpAcrLogAnalyticsWorkspaceName string = ''

@description('SKU for the OCP ACR diagnostics Log Analytics workspace')
param ocpAcrLogAnalyticsWorkspaceSku string = 'PerGB2018'

@description('Retention in days for the OCP ACR diagnostics Log Analytics workspace. Azure requires 30-730 days.')
@minValue(30)
@maxValue(730)
param ocpAcrLogAnalyticsWorkspaceRetentionInDays int = 90

@description('Enable the ACR overview dashboard (request volume, result codes, top repositories, latency, and 429 throttling) for the OCP ACR. Requires ocpAcrDiagnosticSettingsEnabled.')
param ocpAcrOverviewDashboardEnabled bool = false

@description('Name of the Azure portal dashboard resource for OCP ACR overview/throttling visibility')
param ocpAcrOverviewDashboardName string = ''

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

//
//   O C P   A C R
//

module ocpAcr '../modules/acr/acr.bicep' = {
  name: ocpAcrName
  params: {
    acrName: ocpAcrName
    acrSku: ocpAcrSku
    retentionPolicy: {
      enabled: ocpAcrUntaggedImagesRetentionEnabled
      days: ocpAcrUntaggedImagesRetentionDays
    }
    location: location
    zoneRedundant: determineZoneRedundancyForRegion(location, ocpAcrZoneRedundantMode)
  }
}

module ocpCaching '../modules/acr/cache.bicep' = {
  name: '${ocpAcrName}-caching'
  params: {
    acrName: ocpAcrName
    location: location
    quayRepositoriesToCache: [
      {
        ruleName: 'openshiftReleaseDev'
        sourceRepo: 'quay.io/openshift-release-dev/*'
        targetRepo: 'openshift-release-dev/*'
        userIdentifier: 'quay-username'
        passwordIdentifier: 'quay-password'
        loginServer: 'quay.io'
      }
      {
        ruleName: 'redhat-user-workloads-crt-redhat-acm-tenant'
        sourceRepo: 'quay.io/redhat-user-workloads/crt-redhat-acm-tenant/*'
        targetRepo: 'quay-cache/redhat-user-workloads/crt-redhat-acm-tenant/*'
        userIdentifier: 'quay-username'
        passwordIdentifier: 'quay-password'
        loginServer: 'quay.io'
      }
    ]
    keyVaultName: globalKeyVaultName
  }
  dependsOn: [
    ocpAcr
  ]
}

module ocpRedhatProdCaching '../modules/acr/cache.bicep' = {
  name: '${ocpAcrName}-redhat-prod-caching'
  params: {
    acrName: ocpAcrName
    location: location
    quayRepositoriesToCache: [
      {
        ruleName: 'redhat-prod-redhat-operator-index'
        sourceRepo: 'quay.io/redhat-prod/redhat----redhat-operator-index'
        targetRepo: 'rrio/redhat/redhat-operator-index'
        userIdentifier: 'redhat-prod-quay-username'
        passwordIdentifier: 'redhat-prod-quay-password'
        loginServer: 'quay.io'
      }
    ]
    keyVaultName: globalKeyVaultName
  }
  dependsOn: [
    ocpAcr
  ]
}

module ocpRedhatPendingCaching '../modules/acr/cache.bicep' = {
  name: '${ocpAcrName}-redhat-pending-caching'
  params: {
    acrName: ocpAcrName
    location: location
    quayRepositoriesToCache: [
      {
        ruleName: 'redhat-pending-certified-operator-index'
        sourceRepo: 'quay.io/redhat-pending/redhat----certified-operator-index'
        targetRepo: 'rrio/redhat/certified-operator-index'
        userIdentifier: 'redhat-pending-quay-username'
        passwordIdentifier: 'redhat-pending-quay-password'
        loginServer: 'quay.io'
      }
      {
        ruleName: 'redhat-pending-community-operator-index'
        sourceRepo: 'quay.io/redhat-pending/redhat----community-operator-index'
        targetRepo: 'rrio/redhat/community-operator-index'
        userIdentifier: 'redhat-pending-quay-username'
        passwordIdentifier: 'redhat-pending-quay-password'
        loginServer: 'quay.io'
      }
      {
        ruleName: 'redhat-pending-redhat-marketplace-index'
        sourceRepo: 'quay.io/redhat-pending/redhat----redhat-marketplace-index'
        targetRepo: 'rrio/redhat/redhat-marketplace-index'
        userIdentifier: 'redhat-pending-quay-username'
        passwordIdentifier: 'redhat-pending-quay-password'
        loginServer: 'quay.io'
      }
    ]
    keyVaultName: globalKeyVaultName
  }
  dependsOn: [
    ocpAcr
  ]
}

//
//   S V C   A C R
//

module svcAcr '../modules/acr/acr.bicep' = {
  name: svcAcrSku
  params: {
    acrName: svcAcrName
    acrSku: svcAcrSku
    retentionPolicy: {
      enabled: svcAcrUntaggedImagesRetentionEnabled
      days: svcAcrUntaggedImagesRetentionDays
    }
    location: location
    zoneRedundant: determineZoneRedundancyForRegion(location, svcAcrZoneRedundantMode)
  }
}

module svcCaching '../modules/acr/cache.bicep' = {
  name: '${svcAcrName}-caching'
  params: {
    acrName: svcAcrName
    location: location
    quayRepositoriesToCache: [
      {
        ruleName: 'acm-d-multicluster-engine'
        sourceRepo: 'quay.io/acm-d/*'
        targetRepo: 'acm-d-cache/*'
        userIdentifier: 'acm-d-username'
        passwordIdentifier: 'acm-d-password'
        loginServer: 'quay.io'
      }
    ]
    keyVaultName: globalKeyVaultName
  }
  dependsOn: [
    svcAcr
  ]
}

module svcPublicCaching '../modules/acr/public-cache.bicep' = {
  name: '${svcAcrName}-public-caching'
  params: {
    acrName: svcAcrName
    publicRepositoriesToCache: [
      {
        ruleName: 'k8s-ingress-nginx'
        sourceRepo: 'registry.k8s.io/ingress-nginx/*'
        targetRepo: 'k8s-cache/ingress-nginx/*'
      }
      {
        ruleName: 'quay-thanos'
        sourceRepo: 'quay.io/thanos/*'
        targetRepo: 'thanos/*'
      }
    ]
  }
  dependsOn: [
    svcAcr
  ]
}

module miseArtifactSync '../modules/acr/mcr-artifact-sync.bicep' = if (deployMiseArtifactSync) {
  name: 'mise-artifact-sync'
  params: {
    acrName: svcAcrName
    artifactSyncRuleName: 'miseArtifactSync'
    sourceRepositoryPath: 'mcr.microsoft.com/msftonly/mise/mise-1p-container-image'
    targetRepositoryName: 'mise-1p-container-image'
    artifactSyncStatus: 'Active'
  }
  dependsOn: [
    svcAcr
  ]
}

module globalMSISvcAcrAccess '../modules/acr/acr-permissions.bicep' = {
  name: '${globalMSIName}-svc-acr-access'
  params: {
    principalIds: [globalMSI.properties.principalId]
    grantPushAccess: true
    grantPullAccess: true
    acrName: svcAcrName
  }
  dependsOn: [
    svcAcr
  ]
}

module globalMSIOcpAcrAccess '../modules/acr/acr-permissions.bicep' = {
  name: '${globalMSIName}-ocp-acr-access'
  params: {
    principalIds: [globalMSI.properties.principalId]
    grantPushAccess: true
    grantPullAccess: true
    acrName: ocpAcrName
  }
  dependsOn: [
    ocpAcr
  ]
}

module ocpAcrLogAnalyticsWorkspace '../modules/monitor/log-analytics-workspace.bicep' = if (ocpAcrDiagnosticSettingsEnabled) {
  // The OCP ACR (ocpAcrName) is shared by every dev-cloud environment that
  // enables this flag; ocpAcrLogAnalyticsWorkspaceName is configured to
  // resolve to the same static value in every one of them (see config.yaml),
  // so this module name resolves identically too and each environment's
  // redeploy idempotently updates the same workspace instead of creating a
  // new one (Azure caps diagnosticSettings at 5 per resource - a per-env
  // workspace/name would blow past that with 6 dev-cloud environments).
  name: '${ocpAcrName}-diagnostics-workspace'
  params: {
    workspaceName: ocpAcrLogAnalyticsWorkspaceName
    location: location
    sku: ocpAcrLogAnalyticsWorkspaceSku
    retentionInDays: ocpAcrLogAnalyticsWorkspaceRetentionInDays
  }
}

module ocpAcrDiagnosticSettings '../modules/acr/diagnostic-settings.bicep' = if (ocpAcrDiagnosticSettingsEnabled) {
  // Same rationale as ocpAcrLogAnalyticsWorkspace above: this resolves to one
  // shared diagnosticSettings resource on the shared ACR, not one per
  // environment.
  name: '${ocpAcrName}-diagnostic-settings'
  params: {
    acrName: ocpAcrName
    logAnalyticsWorkspaceId: ocpAcrLogAnalyticsWorkspace!.outputs.workspaceId
    diagnosticSettingsName: '${ocpAcrName}-diagnostic-logs'
  }
  dependsOn: [
    ocpAcr
  ]
}

module ocpAcrOverviewDashboard '../modules/monitor/acr-dashboard.bicep' = if (ocpAcrOverviewDashboardEnabled) {
  // Same rationale as ocpAcrLogAnalyticsWorkspace/ocpAcrDiagnosticSettings
  // above: one shared dashboard for the shared ACR, not one per environment.
  name: '${ocpAcrName}-overview-dashboard'
  params: {
    dashboardName: ocpAcrOverviewDashboardName
    location: location
    dashboardTitle: 'ACR ${ocpAcrName} Overview'
    logAnalyticsWorkspaceId: ocpAcrLogAnalyticsWorkspace!.outputs.workspaceId
  }
}

