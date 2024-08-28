using '../templates/region.bicep'

param persist = true

// dns
param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'cs-integ-global'

// maestro
param maestroKeyVaultName = 'maestro-kv-cs-integ'
param maestroEventGridNamespacesName = 'maestro-eventgrid-cs-integ'
param maestroEventGridMaxClientSessionsPerAuthName = 4

// This parameter is always overriden in the Makefile
param currentUserId = ''
