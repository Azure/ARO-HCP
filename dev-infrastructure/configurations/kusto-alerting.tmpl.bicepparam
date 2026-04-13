using '../templates/kusto-alerting.bicep'

param kustoClusterId = '__kustoClusterId__'
param kustoUri = '__kustoUri__'
param kustoName = '{{ .kusto.kustoName }}'
param slActionGroupId = '__slActionGroupId__'
param serviceLogsDatabase = '{{ .kusto.serviceLogsDatabase }}'
param hostedControlPlaneLogsDatabase = '{{ .kusto.hostedControlPlaneLogsDatabase }}'
