// TRANSIENT: STG-global "V2" copy of global-image-sync.tmpl.bicepparam. Identical
// to the canonical file except the globally-unique resource names are sourced from
// the transient stgGlobalV2 block. Removed at decommission.
using '../templates/global-image-sync.bicep'

param containerAppEnvName = '{{ .imageSync.environmentName }}'
param jobNamePrefix = '{{ .imageSync.ocMirror.jobNamePrefix }}'
param containerAppOutboundServiceTags = '{{ .imageSync.outboundServiceTags }}'
param location = '{{ .global.region }}'

param acrResourceGroup = '{{ .global.rg }}'
param keyVaultName = '{{ .stgGlobalV2.globalKeyVaultName }}'

param svcAcrName = '{{ .stgGlobalV2.acrSvcName }}'

param ocpAcrName = '{{ .stgGlobalV2.acrOcpName }}'
param ocpPullSecretName = '{{ .imageSync.ocMirror.pullSecretName }}'
param ocMirrorImage = '{{ .stgGlobalV2.acrSvcName }}.azurecr.io/{{ .imageSync.ocMirror.image.repository }}@{{ .imageSync.ocMirror.image.digest }}'
param ocMirrorEnabled = {{ .imageSync.ocMirror.enabled }}
param operatorVersionsToMirror = '{{ .imageSync.ocMirror.operatorVersionsToMirror }}'
