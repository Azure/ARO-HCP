using '../modules/metrics/metrics.bicep'

param monitorName = 'aro-hcp-monitor-${take(uniqueString(readEnvironmentVariable('CURRENTUSER', '')), 5)}'
param grafanaName = take('aro-hcp-grafana-${uniqueString(readEnvironmentVariable('CURRENTUSER', ''))}', 23)
param msiName = 'aro-hcp-metrics-msi-${take(uniqueString(readEnvironmentVariable('CURRENTUSER', '')), 5)}'

// overriden in makefile
param globalResourceGroup = ''
param currentPrincipalID = ''
param currentPrincipalType = ''
