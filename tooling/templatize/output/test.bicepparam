// copy from dev-infrastructure/configurations/region.bicepparam
using '../templates/region.bicep'

// dns
param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'global'

// maestro
param maestroKeyVaultName = 'maestro-kv-chiac-taiwan'
param maestroEventGridNamespacesName = 'maestro-eventgrid-taiwan'
param maestroEventGridMaxClientSessionsPerAuthName = 4

// These parameters are always overriden in the Makefile
param currentUserId = ''
