using '../templates/kusto-grant-ingest.bicep'

param arobitKustoEnabled = {{ .arobit.kusto.enabled }}

param svcDatabaseName = '{{ .kusto.serviceLogsDatabase }}'
param hcpDatabaseName = '{{ .kusto.hostedControlPlaneLogsDatabase }}'

param kustoResourceId = '__kustoResourceId__'

param clusterLogPrincipalId = '__clusterLogPrincipalId__'
param adminApiPrincipalId = '__adminApiPrincipalId__'

param clusterType = 'svc'
