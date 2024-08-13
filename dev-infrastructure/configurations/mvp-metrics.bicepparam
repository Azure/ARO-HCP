using '../modules/metrics/metrics.bicep'

param grafanaName = 'aro-hcp-grafana'
param msiName = 'aro-hcp-metrics-msi'

// overriden in makefile
param globalResourceGroup = ''
