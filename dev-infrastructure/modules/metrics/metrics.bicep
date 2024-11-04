// this module is only used in dev
@description('Captures logged in users UID')
param currentUserId string = ''

@description('Metrics global resource group name')
param globalResourceGroup string

@description('Captures logged in user principal ID')
param currentPrincipalID string

@description('Captures logged in user principal type')
param currentPrincipalType string

@description('Metrics global MSI name')
param msiName string = take('metrics-admin-${uniqueString(currentUserId)}', 20)

@description('Metrics regional monitor name')
param monitorName string = take('aro-hcp-monitor-${uniqueString(currentUserId)}', 23)

@description('Metrics global Grafana name')
param grafanaName string = take('aro-hcp-grafana-${uniqueString(currentUserId)}', 23)

var grafanaAdmin = {
  principalId: currentPrincipalID
  principalType: currentPrincipalType
}

module grafana 'br:arointacr.azurecr.io/grafana.bicep:metrics.20240814.1' = {
  name: 'grafana'
  params: {
    msiName: msiName
    grafanaName: grafanaName
    grafanaAdmin: grafanaAdmin
  }
}

module monitor 'br:arointacr.azurecr.io/monitor.bicep:monitor.20241004.1' = {
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
