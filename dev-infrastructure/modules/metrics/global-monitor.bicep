@description('The grafana instance to integrate with')
param grafanaResourceId string

@description('Global fleet metrics workspace name')
param monitorName string

@description('Location for the global AMW (should be the global services region)')
param location string = resourceGroup().location

import * as res from '../resource.bicep'

var grafanaRef = res.grafanaRefFromId(grafanaResourceId)

resource monitor 'microsoft.monitor/accounts@2021-06-03-preview' = {
  name: monitorName
  location: location
  tags: {
    aroHCPPurpose: 'fleet-metrics'
  }
}

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
