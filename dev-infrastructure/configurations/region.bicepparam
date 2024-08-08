using '../templates/region.bicep'

// dns
param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'global'

// maestro
param maestroKeyVaultName = take('maestro-kv-${uniqueString(currentUserId)}', 24)
param maestroEventGridNamespacesName = take('maestro-eg-${uniqueString(currentUserId)}', 24)
param maestroEventGridMaxClientSessionsPerAuthName = 4

// metrics
param grafanaName = take('aro-hcp-grafana-${uniqueString(currentUserId)}', 23)

// These parameters are always overriden in the Makefile
param currentUserId = ''
