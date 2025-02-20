// this module is only used in dev

@description('The grafana instance to integrate with')
param grafanaResourceId string

@description('Metrics regional monitor name')
param monitorName string

module monitor 'monitor.bicep' = {
  name: 'monitor'
  params: {
    grafanaResourceId: grafanaResourceId
    monitorName: monitorName
  }
}

output monitorId string = monitor.outputs.monitorId
output monitorPrometheusQueryEndpoint string = monitor.outputs.monitorPrometheusQueryEndpoint
