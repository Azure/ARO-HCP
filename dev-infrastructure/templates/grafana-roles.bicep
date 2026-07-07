@description('Metrics global Grafana name')
param grafanaName string

@description('The global MSI name')
param globalMSIName string

@description('List of grafana role assignments as a space-separated list of items in the format of "principalId/principalType/role"')
param grafanaRoles string

@description('The name of the global AFD instance (empty to skip AFD permissions)')
param azureFrontDoorProfileName string = ''

resource globalMSI 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: globalMSIName
}

module grafana '../modules/grafana/instance.bicep' = {
  name: 'grafana'
  params: {
    grafanaName: grafanaName
    grafanaManagerPrincipalId: globalMSI.properties.principalId
    grafanaRoles: grafanaRoles
  }
}

resource frontDoorProfile 'Microsoft.Cdn/profiles@2023-05-01' existing = if (!empty(azureFrontDoorProfileName)) {
  name: azureFrontDoorProfileName
}

module grafanaAfdPermissions '../modules/grafana/observability-permissions.bicep' = if (!empty(azureFrontDoorProfileName)) {
  name: 'grafana-afd-permissions'
  params: {
    grafanaPrincipalId: grafana.outputs.grafanaPrincipalId
    frontDoorProfileId: frontDoorProfile.id
  }
}

output grafanaPrincipalId string = grafana.outputs.grafanaPrincipalId
