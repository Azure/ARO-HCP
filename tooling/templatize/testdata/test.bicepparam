// copy from dev-infrastructure/configurations/region.bicepparam
using '../templates/region.bicep'

// dns
param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'global'

// maestro
param maestroKeyVaultNamea = '{{ .region_maestro_keyvault }}'
param maestroEventGridNamespacesName = '{{ .region_eventgrid_namespace }}'
param maestroEventGridMaxClientSessionsPerAuthName = 4

// These parameters are always overriden in the Makefile
param currentUserId = ''
