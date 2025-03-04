using '../templates/global-image-sync.bicep'

param containerAppEnvName = '{{ .imageSync.environmentName }}'

param acrResourceGroup = '{{ .imageSync.acrRG }}'
param keyVaultName = '{{ .imageSync.keyVault.name}}'
param keyVaultPrivate = {{ .imageSync.keyVault.private }}
param keyVaultSoftDelete = {{ .imageSync.keyVault.softDelete }}

param componentSyncPullSecretName = '{{ .imageSync.componentSync.pullSecretName }}'
param componentSyncImage = '{{ .svcAcrName }}.azurecr.io/{{ .imageSync.componentSync.image.repository }}@{{ .imageSync.componentSync.image.digest }}'
param componentSyncEnabed = {{ .imageSync.componentSync.enabled }}
param componentSyncSecrets = '{{ .imageSync.componentSync.secrets }}'

param svcAcrName = '{{ .svcAcrName }}'

param ocpAcrName = '{{ .ocpAcrName }}'
param ocpPullSecretName = '{{ .imageSync.ocMirror.pullSecretName }}'
param repositoriesToSync = '{{ .imageSync.componentSync.repositories }}'
param ocMirrorImage = '{{ .svcAcrName }}.azurecr.io/{{ .imageSync.ocMirror.image.repository }}@{{ .imageSync.ocMirror.image.digest }}'
param ocMirrorEnabled = {{ .imageSync.ocMirror.enabled }}

param numberOfTags = 10
