using '../templates/opstool-alerting.bicep'

param workloadKVName = '{{ .opstool.keyVault.name }}'
param alertingEnabled = {{ .opstool.alerting.enabled }}
