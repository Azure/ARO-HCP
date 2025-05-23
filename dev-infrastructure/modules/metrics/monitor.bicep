@description('The grafana instance to integrate with')
param grafanaResourceId string

@description('Metrics region monitor name')
param monitorName string

import * as res from '../resource.bicep'

var grafanaRef = res.grafanaRefFromId(grafanaResourceId)

resource monitor 'microsoft.monitor/accounts@2021-06-03-preview' = {
  name: monitorName
  location: resourceGroup().location
}

// Assign the Monitoring Data Reader role to the Azure Managed Grafana system-assigned managed identity at the workspace scope
var dataReader = 'b0d8363b-8ddd-447d-831f-62ca05bff136'

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' existing = {
  name: grafanaRef.name
  scope: resourceGroup(grafanaRef.resourceGroup.subscriptionId, grafanaRef.resourceGroup.name)
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(monitor.id, grafana.id, dataReader)
  scope: monitor
  properties: {
    principalId: grafana.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dataReader)
  }
}

output monitorId string = monitor.id
output monitorPrometheusQueryEndpoint string = monitor.properties.metrics.prometheusQueryEndpoint
