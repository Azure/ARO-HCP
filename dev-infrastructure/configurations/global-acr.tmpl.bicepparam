using '../templates/global-acr.bicep'

param svcAcrName = '{{ .acr.svc.name }}'
param svcAcrSku = 'Premium'
param svcAcrUntaggedImagesRetentionEnabled = {{ .acr.svc.untaggedImagesRetention.enabled }}
param svcAcrUntaggedImagesRetentionDays = {{ .acr.svc.untaggedImagesRetention.days }}

param ocpAcrName = '{{ .acr.ocp.name }}'
param ocpAcrSku = 'Premium'
param ocpAcrUntaggedImagesRetentionEnabled = {{ .acr.ocp.untaggedImagesRetention.enabled }}
param ocpAcrUntaggedImagesRetentionDays = {{ .acr.ocp.untaggedImagesRetention.days }}

param globalMSIName = '{{ .global.globalMSIName }}'

param location = '{{ .global.region }}'

param svcAcrZoneRedundantMode = '{{ .acr.svc.zoneRedundantMode }}'
param ocpAcrZoneRedundantMode = '{{ .acr.ocp.zoneRedundantMode }}'

param globalKeyVaultName = '{{ .global.keyVault.name}}'

param deployMiseArtifactSync = {{ .mise.deploy }}
