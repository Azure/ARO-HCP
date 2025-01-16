// this module is only used in dev
@description('Metrics global resource group name')
param globalResourceGroup string

@description('Metrics global MSI name')
param msiName string

@description('Metrics regional monitor name')
param monitorName string

@description('Metrics global Grafana name')
param grafanaName string

module monitor 'monitor.bicep' = {
  name: 'monitor'
  params: {
    globalResourceGroup: globalResourceGroup
    msiName: msiName
    monitorName: monitorName
    grafanaName: grafanaName
  }
}

output msiId string = monitor.outputs.msiId
output monitorId string = monitor.outputs.monitorId
