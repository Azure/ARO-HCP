using '../templates/region.bicep'

param persist = true

// dns
param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'global'

// maestro
param maestroKeyVaultName = 'maestro-kv-aro-hcp-dev'
param maestroEventGridNamespacesName = 'maestro-eventgrid-aro-hcp-dev'
param maestroEventGridMaxClientSessionsPerAuthName = 4

// metrics
param grafanaName = 'aro-hcp-grafana'

// This parameter is always overriden in the Makefile
param currentUserId = ''
