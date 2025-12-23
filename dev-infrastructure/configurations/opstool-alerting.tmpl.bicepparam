using '../templates/opstool-alerting.bicep'

param alertEmail = '{{ .opstool.alerting.email }}'
param alertingEnabled = {{ .opstool.alerting.enabled }}
