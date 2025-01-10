// this module is only used in dev
@description('Metrics global resource group name')
param globalResourceGroup string

@description('Metrics global MSI name')
param msiName string

@description('Metrics regional monitor name')
param monitorName string

@description('Metrics global Grafana name')
param grafanaName string

@description('The admin group principal ID to manage Grafana')
param grafanaAdminGroupPrincipalId string

var grafanaAdmin = {
  principalId: grafanaAdminGroupPrincipalId
  principalType: 'group'
}

module grafana 'br:arointacr.azurecr.io/grafana.bicep:metrics.20240814.1' = {
  name: 'grafana'
  params: {
    msiName: msiName
    grafanaName: grafanaName
    grafanaAdmin: grafanaAdmin
  }
}

module monitor 'monitor.bicep' = {
  name: 'monitor'
  params: {
    globalResourceGroup: globalResourceGroup
    msiName: msiName
    monitorName: monitorName
    grafanaName: grafanaName
  }
  dependsOn: [
    grafana
  ]
}

output msiId string = monitor.outputs.msiId
output grafanaId string = monitor.outputs.grafanaId
output monitorId string = monitor.outputs.monitorId
