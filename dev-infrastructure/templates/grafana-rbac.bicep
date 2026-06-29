@description('Global Grafana instance name')
param grafanaName string

@description('The global msi name')
param globalMSIName string

@description('List of grafana role assignments as a space-separated list of items in the format of "principalId/principalType/role"')
param grafanaRoles string

@description('The name of the global AFD instance')
param azureFrontDoorProfileName string

@description('Whether Azure Front Door is managed')
param azureFrontDoorManage bool

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

module grafanaRBAC '../modules/grafana/instance.bicep' = {
  name: 'grafana-rbac'
  params: {
    grafanaName: grafanaName
    grafanaManagerPrincipalId: globalMSI.properties.principalId
    grafanaRoles: grafanaRoles
  }
}

resource frontDoorProfile 'Microsoft.Cdn/profiles@2023-05-01' existing = if (azureFrontDoorManage && !empty(azureFrontDoorProfileName)) {
  name: azureFrontDoorProfileName
}

module grafanaAfdPermissions '../modules/grafana/observability-permissions.bicep' = if (azureFrontDoorManage && !empty(azureFrontDoorProfileName)) {
  name: 'grafana-afd-permissions'
  params: {
    grafanaPrincipalId: grafanaRBAC.outputs.grafanaPrincipalId
    frontDoorProfileId: frontDoorProfile.id
  }
}
