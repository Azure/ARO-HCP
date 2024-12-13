using '../templates/image-sync.bicep'

param containerAppEnvName = '{{ .imageSync.environmentName }}'

param acrResourceGroup = '{{ .imageSync.acrRG }}'
param keyVaultName = '{{ .imageSync.keyVault.name}}'
param keyVaultPrivate = {{ .imageSync.keyVault.private }}
param keyVaultSoftDelete = {{ .imageSync.keyVault.softDelete }}

param componentSyncPullSecretName = 'component-sync-pull-secret'
param componentSyncImage = '{{ .svcAcrName }}.azurecr.io/{{ .imageSync.componentSync.imageRepo }}:{{ .imageSync.componentSync.imageTag }}'
param componentSyncEnabed = {{ .imageSync.componentSync.enabled }}
param componentSyncSecrets = '{{ .imageSync.componentSync.secrets }}'

param svcAcrName = '{{ .svcAcrName }}'

param ocpAcrName = '{{ .ocpAcrName }}'
param ocpPullSecretName = 'pull-secret'
param repositoriesToSync = '{{ .imageSync.componentSync.repositories }}'
param ocMirrorImage = '{{ .svcAcrName }}.azurecr.io/{{ .imageSync.ocMirror.imageRepo }}:{{ .imageSync.ocMirror.imageTag }}'
param ocMirrorEnabled = {{ .imageSync.ocMirror.enabled }}

param numberOfTags = 10
