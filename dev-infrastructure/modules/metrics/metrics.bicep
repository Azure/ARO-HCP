// this module is only used in dev
@description('Captures logged in users UID')
param currentUserId string = ''

@description('Metrics global resource group name')
param globalResourceGroup string

@description('Metrics global MSI name')
param msiName string = take('metrics-admin-${uniqueString(currentUserId)}', 4)

@description('Metrics global Grafana name')
param grafanaName string = take('aro-hcp-grafana-${uniqueString(currentUserId)}', 23)

var grafanaAdmin = {
  principalId: '6b6d3adf-8476-4727-9812-20ffdef2b85c' // aro-hcp-engineering-App Developer
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

module monitor 'br:arointacr.azurecr.io/monitor.bicep:metrics.20240814.2' = {
  name: 'monitor'
  params: {
    globalResourceGroup: globalResourceGroup
    msiName: msiName
    grafanaName: grafanaName
  }
  dependsOn: [
    grafana
  ]
}

output msiId string = monitor.outputs.msiId
output grafanaId string = monitor.outputs.grafanaId
output monitorId string = monitor.outputs.monitorId
