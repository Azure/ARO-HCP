using '../templates/global-image-sync.bicep'

param containerAppEnvName = '{{ .imageSync.environmentName }}'
param containerAppOutboundServiceTags = '{{ .imageSync.outboundServiceTags }}'
param location = '{{ .global.region }}'

param acrResourceGroup = '{{ .global.rg }}'
param keyVaultName = '{{ .global.keyVault.name }}'

param svcAcrName = '{{ .acr.svc.name }}'

param ocpAcrName = '{{ .acr.ocp.name }}'
param ocpPullSecretName = '{{ .imageSync.ocMirror.pullSecretName }}'
param ocMirrorImage = '{{ .acr.svc.name }}.azurecr.io/{{ .imageSync.ocMirror.image.repository }}@{{ .imageSync.ocMirror.image.digest }}'
param ocMirrorEnabled = {{ .imageSync.ocMirror.enabled }}

