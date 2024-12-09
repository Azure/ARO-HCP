param ocpAcrName string
param ocpAcrSku string

param svcAcrName string
param svcAcrSku string

param location string

module ocpAcr '../modules/acr/acr.bicep' = {
  name: '${deployment().name}-${ocpAcrName}'
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
