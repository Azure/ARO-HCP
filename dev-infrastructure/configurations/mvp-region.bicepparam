using '../templates/region.bicep'

// maestro
param maestroKeyVaultName = 'maestro-kv-aro-hcp-dev'
param maestroEventGridNamespacesName = 'maestro-eventgrid-aro-hcp-dev'
param maestroEventGridMaxClientSessionsPerAuthName = 4

// This parameter is always overriden in the Makefile
param currentUserId = ''
