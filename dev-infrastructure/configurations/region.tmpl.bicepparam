using '../templates/region.bicep'

// dns
param baseDNSZoneName = '{{ .baseDnsZoneName }}'
param baseDNSZoneResourceGroup = '{{ .baseDnsZoneRG }}'
param regionalDNSSubdomain = '{{ .regionalDNSSubdomain }}'

// maestro
param maestroKeyVaultName = '{{ .maestro.keyVaultName }}'
param maestroEventGridNamespacesName = '{{ .maestro.eventgridName }}'
param maestroEventGridMaxClientSessionsPerAuthName = {{ .maestro.eventGridMaxClientSessionsPerAuthName }}
