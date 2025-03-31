/*
Sets up the global ACRs for SVC and OCP images.
*/
import { determineZoneRedundancyForRegion } from '../modules/common.bicep'

param ocpAcrName string
param ocpAcrSku string

param svcAcrName string
param svcAcrSku string

param globalMSIName string

param location string

@description('The zone redundancy mode for the OCP ACR')
param ocpAcrZoneRedundantMode string

@description('The zone redundancy mode for the SVC ACR')
param svcAcrZoneRedundantMode string

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

module ocpAcr '../modules/acr/acr.bicep' = {
  name: ocpAcrName
  params: {
    acrName: ocpAcrName
    acrSku: ocpAcrSku
    location: location
    zoneRedundant: determineZoneRedundancyForRegion(location, ocpAcrZoneRedundantMode)
  }
}

module svcAcr '../modules/acr/acr.bicep' = {
  name: svcAcrSku
  params: {
    acrName: svcAcrName
    acrSku: svcAcrSku
    location: location
    zoneRedundant: determineZoneRedundancyForRegion(location, svcAcrZoneRedundantMode)
  }
}

module globalMsiSvcAcrAccess '../modules/acr/acr-permissions.bicep' = {
  name: '${globalMSIName}-svc-acr-access'
  params: {
    principalId: globalMSI.properties.principalId
    grantPushAccess: true
    grantPullAccess: true
    acrName: svcAcrName
  }
  dependsOn: [
    svcAcr
  ]
}
