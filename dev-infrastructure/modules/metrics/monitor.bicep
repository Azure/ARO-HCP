@description('The grafana instance to integrate with')
param grafanaResourceId string

@description('Metrics region monitor name')
param monitorName string

@description('Purpose of the monitor')
param purpose string

@description('Use the internal Microsoft API version for the Azure Monitor Workspace')
param useInternalApiVersion bool = false

import * as res from '../resource.bicep'

var grafanaRef = res.grafanaRefFromId(grafanaResourceId)
var hasGrafanaResourceId = !empty(grafanaResourceId)

resource monitor 'microsoft.monitor/accounts@2021-06-03-preview' = if (!useInternalApiVersion) {
  name: monitorName
  location: resourceGroup().location
  tags: {
    aroHCPPurpose: purpose
  }
}

resource monitorInternal 'microsoft.monitor/accounts@2021-06-01-preview' = if (useInternalApiVersion) {
  name: monitorName
  location: resourceGroup().location
  tags: {
    aroHCPPurpose: purpose
  }
}

// Assign the Monitoring Data Reader role to the Azure Managed Grafana system-assigned managed identity at the workspace scope
var dataReader = 'b0d8363b-8ddd-447d-831f-62ca05bff136'

resource grafana 'Microsoft.Dashboard/grafana@2023-09-01' existing = if (hasGrafanaResourceId) {
  name: grafanaRef.name
  scope: resourceGroup(grafanaRef.resourceGroup.subscriptionId, grafanaRef.resourceGroup.name)
}

resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (hasGrafanaResourceId && !useInternalApiVersion) {
  name: guid(monitor.id, grafana!.id, dataReader)
  scope: monitor
  properties: {
    principalId: grafana!.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dataReader)
  }
}

resource roleAssignmentInternal 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (hasGrafanaResourceId && useInternalApiVersion) {
  name: guid(monitorInternal.id, grafana!.id, dataReader)
  scope: monitorInternal
  properties: {
    principalId: grafana!.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', dataReader)
  }
}

output monitorId string = useInternalApiVersion ? monitorInternal.id : monitor.id
output monitorPrometheusQueryEndpoint string = useInternalApiVersion
  ? monitorInternal.properties.metrics.prometheusQueryEndpoint
  : monitor.properties.metrics.prometheusQueryEndpoint
