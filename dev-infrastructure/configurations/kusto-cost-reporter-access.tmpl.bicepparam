using '../templates/kusto-cost-reporter-access.bicep'

param identityName = '{{ .kusto.costReporting.identityName }}'
param identityResourceGroup = '{{ .global.rg }}'
param kustoName = '{{ .kusto.kustoName }}'
param databaseNames = [
  '{{ .kusto.serviceLogsDatabase }}'
  '{{ .kusto.hostedControlPlaneLogsDatabase }}'
  '{{ .kusto.monitoringEventsDatabase }}'
]
