using '../templates/global-image-sync.bicep'

param containerAppEnvName = '{{ .imageSync.environmentName }}'
param containerAppOutboundServiceTags = '{{ .imageSync.outboundServiceTags }}'
param location = '{{ .global.region }}'

param acrResourceGroup = '{{ .global.rg }}'
param keyVaultName = '{{ .global.keyVault.name }}'

param componentSyncPullSecretName = '{{ .imageSync.componentSync.pullSecretName }}'
param componentSyncImage = '{{ .acr.svc.name }}.azurecr.io/{{ .imageSync.componentSync.image.repository }}@{{ .imageSync.componentSync.image.digest }}'
param componentSyncEnabed = {{ .imageSync.componentSync.enabled }}
param componentSyncSecrets = '{{ .imageSync.componentSync.secrets }}'

param svcAcrName = '{{ .acr.svc.name }}'

param ocpAcrName = '{{ .acr.ocp.name }}'
param ocpPullSecretName = '{{ .imageSync.ocMirror.pullSecretName }}'
param repositoriesToSync = '{{ .imageSync.componentSync.repositories }}'
param ocMirrorImage = '{{ .acr.svc.name }}.azurecr.io/{{ .imageSync.ocMirror.image.repository }}@{{ .imageSync.ocMirror.image.digest }}'
param ocMirrorEnabled = {{ .imageSync.ocMirror.enabled }}

param numberOfTags = 10
