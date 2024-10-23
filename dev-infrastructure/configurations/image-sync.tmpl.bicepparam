using '../templates/image-sync.bicep'

param containerAppEnvName = '{{ .imageSyncEnvironmentName }}'

param acrResourceGroup = '{{ .imageSyncAcrRG }}'
param keyVaultName = '{{ .serviceKeyVaultName}}'
param keyVaultResourceGroup = '{{ .serviceKeyVaultRG }}'

param bearerSecretName = 'bearer-secret'
param componentSyncPullSecretName = 'component-sync-pull-secret'
param componentSyncImage = '{{ .svcAcrName }}.azurecr.io/{{ .imageSyncImageRepo }}:{{ .imageSyncImageTag }}'
param svcAcrName = '{{ .svcAcrName }}'

param ocpAcrName = '{{ .ocpAcrName }}'
param ocpPullSecretName = 'pull-secret'
param repositoriesToSync = '{{ .imageSyncRepositories }}'
param ocMirrorImage = '{{ .svcAcrName }}.azurecr.io/{{ .ocMirrorImageRepo }}:{{ .ocMirrorImageTag }}'
param numberOfTags = 10
