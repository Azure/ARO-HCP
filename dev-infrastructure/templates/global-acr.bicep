/*
Sets up the global ACRs for SVC and OCP images.
*/

param ocpAcrName string
param ocpAcrSku string
param ocpAcrZoneRedundancy string

param svcAcrName string
param svcAcrSku string
param svcAcrZoneRedundancy string

param location string

module ocpAcr '../modules/acr/acr.bicep' = {
  name: ocpAcrName
  params: {
    acrName: ocpAcrName
    acrSku: ocpAcrSku
    location: location
    zoneRedundancy: ocpAcrZoneRedundancy
  }
}

module svcAcr '../modules/acr/acr.bicep' = {
  name: svcAcrSku
  params: {
    acrName: svcAcrName
    acrSku: svcAcrSku
    location: location
    zoneRedundancy: svcAcrZoneRedundancy
  }
}
