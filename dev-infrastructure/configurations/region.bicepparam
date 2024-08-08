using '../templates/region.bicep'

// dns
param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'global'

// maestro
param maestroKeyVaultName = take('maestro-kv-${uniqueString(currentUserId)}', 24)
param maestroEventGridNamespacesName = take('maestro-eg-${uniqueString(currentUserId)}', 24)
param maestroEventGridMaxClientSessionsPerAuthName = 4

// observability
param managedGrafanaName= take('aro-hcp-${uniqueString(currentUserId)}', 24)

// This parameter is always overriden in the Makefile
param currentUserId = ''
