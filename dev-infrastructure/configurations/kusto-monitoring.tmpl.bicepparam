using '../templates/kusto-monitoring.bicep'

param kustoClusterId = '__kustoClusterId__'
param kustoRegion = '__kustoRegion__'
param regionLocation = '{{ .region }}'
param actionGroupSL = '__actionGroupSL__'
param alertsEnabled = {{ .monitoring.alertsEnabled }}
