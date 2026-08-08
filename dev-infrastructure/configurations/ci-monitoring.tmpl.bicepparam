using '../templates/ci-monitoring.bicep'

param ciWorkspaceName = 'ci-shared-metrics'
param ciHcpWorkspaceName = 'ci-hcp-shared-metrics'
param createHcpWorkspace = true
param prometheusPrincipalId = '__prometheusPrincipalId__'
