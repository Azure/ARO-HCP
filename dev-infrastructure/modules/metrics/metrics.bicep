// this module is only used in dev

@description('The grafana instance to integrate with')
param grafanaResourceId string

@description('Metrics regional monitor name')
param monitorName string

@description('List of emails for Dev Alerting')
param devAlertingEmails string

@description('Comma seperated list of action groups for Sev 1 alerts.')
param sev1ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 2 alerts.')
param sev2ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 3 alerts.')
param sev3ActionGroupIDs string

@description('Comma seperated list of action groups for Sev 4 alerts.')
param sev4ActionGroupIDs string

module monitor 'monitor.bicep' = {
  name: 'monitor'
  params: {
    grafanaResourceId: grafanaResourceId
    monitorName: monitorName
    devAlertingEmails: devAlertingEmails
    sev1ActionGroupIDs: sev1ActionGroupIDs
    sev2ActionGroupIDs: sev2ActionGroupIDs
    sev3ActionGroupIDs: sev3ActionGroupIDs
    sev4ActionGroupIDs: sev4ActionGroupIDs
  }
}

output monitorId string = monitor.outputs.monitorId
output monitorPrometheusQueryEndpoint string = monitor.outputs.monitorPrometheusQueryEndpoint
