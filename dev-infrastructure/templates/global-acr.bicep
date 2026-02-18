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
