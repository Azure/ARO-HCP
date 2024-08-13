using '../modules/metrics/metrics.bicep'

param grafanaName = take('aro-hcp-grafana-${uniqueString(readEnvironmentVariable('CURRENTUSER', ''))}', 23)
param msiName = 'aro-hcp-metrics-msi-${take(uniqueString(readEnvironmentVariable('CURRENTUSER', '')), 5)}'

// overriden in makefile
param globalResourceGroup = ''
