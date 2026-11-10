using '../templates/kusto-grant-ingest.bicep'

param arobitKustoEnabled = {{ .arobit.kusto.enabled }}

param svcDatabaseName = '{{ .kusto.allAroHcpLogsDatabase }}'

param kustoResourceId = '__kustoResourceId__'

param clusterLogPrincipalId = '__clusterLogPrincipalId__'
param adminApiPrincipalId = '__adminApiPrincipalId__'
param exporterPrincipalId = '__exporterPrincipalId__'

param clusterType = 'svc'
