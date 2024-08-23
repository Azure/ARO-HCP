using '../modules/metrics/metrics.bicep'

param grafanaName = 'cs-integ-grafana'
param msiName = 'cs-integ-metrics-msi'

// overriden in makefile
param globalResourceGroup = ''
