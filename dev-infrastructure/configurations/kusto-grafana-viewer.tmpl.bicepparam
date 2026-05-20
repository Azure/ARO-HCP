using '../templates/kusto-grafana-viewer.bicep'

param grafanaResourceId = '__grafanaResourceId__'
param kustoName = '{{ .kusto.kustoName }}'
param databaseName = '{{ .kusto.serviceLogsDatabase }}'
