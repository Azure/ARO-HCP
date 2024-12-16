using '../templates/region.bicep'

// general
param globalRegion = '{{ .global.region }}'
param globalResourceGroup = '{{ .global.rg }}'
param regionalRegion = '{{ .region }}'

// acr
param ocpAcrName = '{{ .ocpAcrName }}'
param svcAcrName = '{{ .svcAcrName }}'

// dns
param baseDNSZoneName = '{{ .baseDnsZoneName }}'
param baseDNSZoneResourceGroup = '{{ .baseDnsZoneRG }}'
param regionalDNSSubdomain = '{{ .regionalDNSSubdomain }}'

// maestro
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param maestroEventGridMaxClientSessionsPerAuthName = {{ .maestro.eventGrid.maxClientSessionsPerAuthName }}
param maestroEventGridPrivate = {{ .maestro.eventGrid.private }}
