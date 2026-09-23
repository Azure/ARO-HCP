// TRANSIENT: STG-global "V2" copy of global-acr.tmpl.bicepparam. Identical to the
// canonical file except the globally-unique resource names are sourced from the
// transient stgGlobalV2 block so the parallel STG-global stack does not collide
// with the live (shared-subscription) names. Removed at decommission.
using '../templates/global-acr.bicep'

param svcAcrName = '{{ .stgGlobalV2.acrSvcName }}'
param svcAcrSku = 'Premium'
param svcAcrUntaggedImagesRetentionEnabled = {{ .acr.svc.untaggedImagesRetention.enabled }}
param svcAcrUntaggedImagesRetentionDays = {{ .acr.svc.untaggedImagesRetention.days }}

param ocpAcrName = '{{ .stgGlobalV2.acrOcpName }}'
param ocpAcrSku = 'Premium'
param ocpAcrUntaggedImagesRetentionEnabled = {{ .acr.ocp.untaggedImagesRetention.enabled }}
param ocpAcrUntaggedImagesRetentionDays = {{ .acr.ocp.untaggedImagesRetention.days }}

param globalMSIName = '{{ .global.globalMSIName }}'

param location = '{{ .global.region }}'

param svcAcrZoneRedundantMode = '{{ .acr.svc.zoneRedundantMode }}'
param ocpAcrZoneRedundantMode = '{{ .acr.ocp.zoneRedundantMode }}'

param globalKeyVaultName = '{{ .stgGlobalV2.globalKeyVaultName }}'

param deployMiseArtifactSync = {{ .mise.deploy }}
