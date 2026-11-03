using '../templates/kusto-cost-reporter-access.bicep'

param enabled = {{ .kusto.costReporting.enabled }}
param identityName = '{{ .kusto.costReporting.identityName }}'
param identityResourceGroup = '{{ .global.rg }}'
param kustoName = '{{ .kusto.kustoName }}'
param databaseNames = [
  '{{ .kusto.serviceLogsDatabase }}'
  '{{ .kusto.hostedControlPlaneLogsDatabase }}'
  '{{ .kusto.monitoringEventsDatabase }}'
]
