using '../templates/image-sync.bicep'

param containerAppEnvName = '{{ .imageSync.environmentName }}'

param acrResourceGroup = '{{ .imageSync.acrRG }}'
param keyVaultName = '{{ .serviceKeyVault.name}}'
param keyVaultResourceGroup = '{{ .serviceKeyVault.rg }}'

param bearerSecretName = 'bearer-secret'
param componentSyncPullSecretName = 'component-sync-pull-secret'
param componentSyncImage = '{{ .svcAcrName }}.azurecr.io/{{ .imageSync.imageRepo }}:{{ .imageSync.imageTag }}'
param svcAcrName = '{{ .svcAcrName }}'

param ocpAcrName = '{{ .ocpAcrName }}'
param ocpPullSecretName = 'pull-secret'
param repositoriesToSync = '{{ .imageSync.repositories }}'
param ocMirrorImage = '{{ .svcAcrName }}.azurecr.io/{{ .ocMirror.imageRepo }}:{{ .ocMirror.imageTag }}'
param numberOfTags = 10
