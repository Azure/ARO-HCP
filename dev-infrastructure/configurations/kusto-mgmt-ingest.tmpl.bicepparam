using '../templates/kusto-grant-ingest.bicep'

param arobitKustoEnabled = {{ .arobit.kusto.enabled }}

param svcDatabaseName = '{{ .kusto.serviceLogsDatabase }}'
param allAroHcpDatabaseName = '{{ .kusto.allAroHcpLogsDatabase }}'

param kustoResourceId = '__kustoResourceId__'

param clusterLogPrincipalId = '__clusterLogPrincipalId__'

param clusterType = 'mgmt'
