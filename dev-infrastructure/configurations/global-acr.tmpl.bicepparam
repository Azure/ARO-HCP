using '../templates/global-acr.bicep'

param svcAcrName = '{{ .svcAcrName }}'
param svcAcrSku = 'Premium'

param ocpAcrName = '{{ .ocpAcrName }}'
param ocpAcrSku = 'Premium'

param location = '{{ .global.region }}'

param ocpAcrZoneRedundancy = 'Enabled'
param svcAcrZoneRedundancy = 'Enabled'
