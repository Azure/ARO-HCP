using '../templates/region.bicep'

// dns
param baseDNSZoneName = '{{ .baseDnsZoneName }}'
param baseDNSZoneResourceGroup = '{{ .baseDnsZoneRG }}'
param regionalDNSSubdomain = '{{ .regionalDNSSubdomain }}'

// maestro
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param maestroEventGridMaxClientSessionsPerAuthName = any('{{ .maestro.eventGrid.maxClientSessionsPerAuthName }}')
param maestroEventGridPrivate = any('{{ .maestro.eventGrid.private }}')
