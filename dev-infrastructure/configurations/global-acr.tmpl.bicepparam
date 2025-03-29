using '../templates/global-acr.bicep'

param svcAcrName = '{{ .acr.svc.name }}'
param svcAcrSku = 'Premium'

param ocpAcrName = '{{ .acr.ocp.name }}'
param ocpAcrSku = 'Premium'

param globalMSIName = '{{ .global.globalMSIName }}'

param location = '{{ .global.region }}'

param svcAcrZoneRedundantMode = '{{ .acr.svc.zoneRedundantMode }}'
param ocpAcrZoneRedundantMode = '{{ .acr.ocp.zoneRedundantMode }}'
