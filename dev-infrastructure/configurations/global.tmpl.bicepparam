using '../templates/global.bicep'

param svcAcrName = '{{ .svcAcrName }}'
param svcAcrSku = 'Premium'

param ocpAcrName = '{{ .ocpAcrName }}'
param ocpAcrSku = 'Premium'

param location = '{{ .global.region }}'
