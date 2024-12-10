/*
Sets up the global ACRs for SVC and OCP images.
*/

param ocpAcrName string
param ocpAcrSku string

param svcAcrName string
param svcAcrSku string

param location string

module ocpAcr '../modules/acr/acr.bicep' = {
  // The provided deployment name '0694A46E80A94DE4907CFD718CABFCB20global-acrs-uksouth-1-arohcpocpint' has a length of '67' which exceeds the maximum length of '64'
  name: ocpAcrName
  params: {
    acrName: ocpAcrName
    acrSku: ocpAcrSku
    location: location
  }
}

module svcAcr '../modules/acr/acr.bicep' = {
  name: '${deployment().name}-${svcAcrSku}'
  params: {
    acrName: svcAcrName
    acrSku: svcAcrSku
    location: location
  }
}
