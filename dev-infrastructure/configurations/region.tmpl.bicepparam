using '../templates/region.bicep'

// general
param globalRegion = '{{ .global.region }}'
param globalResourceGroup = '{{ .global.rg }}'
param regionalRegion = '{{ .region }}'

// acr
param ocpAcrName = '{{ .ocpAcrName }}'
param svcAcrName = '{{ .svcAcrName }}'

// dns
param cxBaseDNSZoneName = '{{ .dns.cxParentZoneName }}'
param svcBaseDNSZoneName = '{{ .dns.svcParentZoneName }}'
param baseDNSZoneResourceGroup = '{{ .dns.baseDnsZoneRG }}'
param regionalDNSSubdomain = '{{ .dns.regionalSubdomain }}'

// maestro
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param maestroEventGridMaxClientSessionsPerAuthName = {{ .maestro.eventGrid.maxClientSessionsPerAuthName }}
param maestroEventGridPrivate = {{ .maestro.eventGrid.private }}
