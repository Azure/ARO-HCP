using '../templates/maestro-consumer.bicep'

param deployMaestroConsumer = true
param maestroKeyVaultName = 'maestro-kv-aro-hcp-dev'
param maestroEventGridNamespacesName = 'maestro-eventgrid-aro-hcp-dev'
param maestroCertDomain = 'selfsigned.maestro.keyvault.aro-dev.azure.com'

param regionalResourceGroup = ''
param mgmtResourceGroup = ''
