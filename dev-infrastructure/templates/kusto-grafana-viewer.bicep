import * as res from '../modules/resource.bicep'

@description('Grafana resource ID for database-level Viewer access')
param grafanaResourceId string

@description('Name of the Kusto cluster')
param kustoName string

@description('Name of the database to grant Viewer access on')
param databaseName string

var grafanaRef = res.grafanaRefFromId(grafanaResourceId)

resource grafana 'Microsoft.Dashboard/grafana@2024-10-01' existing = {
  name: grafanaRef.name
  scope: resourceGroup(grafanaRef.resourceGroup.subscriptionId, grafanaRef.resourceGroup.name)
}

module grantViewer '../modules/logs/kusto/grant-access.bicep' = {
  name: 'grafana-serviceLogs-viewer'
  params: {
    kustoName: kustoName
    databaseName: databaseName
    readAccessPrincipalIds: [grafana.identity.principalId]
  }
}
