using '../templates/ci-monitoring.bicep'

param ciWorkspaceName = 'ci-shared-metrics'
param ciHcpWorkspaceName = 'ci-hcp-shared-metrics'
param grafanaResourceId = '__grafanaResourceId__'
param createHcpWorkspace = true
