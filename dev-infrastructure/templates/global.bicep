param ocpAcrName string
param ocpAcrSku string

param svcAcrName string
param svcAcrSku string

param location string

param manageTokenRole bool

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

module tokenMgmtRole '../modules/acr/token-mgmt-role.bicep' = if (manageTokenRole) {
  name: 'acr-token-mgmt-role'
  scope: subscription()
}
