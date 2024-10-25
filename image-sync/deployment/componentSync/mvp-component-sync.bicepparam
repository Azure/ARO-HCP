using 'component-sync.bicep'

param environmentName = 'image-sync-env-sxo4oqbcjiekg'

param jobName = 'component-sync'

param containerImage = 'arohcpdev.azurecr.io/image-sync/component-sync:latest'

param imageSyncManagedIdentity = 'image-sync-sxo4oqbcjiekg'

param acrDnsName = 'arohcpdev.azurecr.io'

param pullSecretUrl = 'https://aro-hcp-dev-global-kv.vault.azure.net/secrets/component-sync-pull-secret'

param bearerSecretUrl = 'https://aro-hcp-dev-global-kv.vault.azure.net/secrets/bearer-secret'
