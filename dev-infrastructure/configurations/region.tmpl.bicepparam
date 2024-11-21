using '../templates/region.bicep'

// dns
param baseDNSZoneName = '{{ .baseDnsZoneName }}'
param baseDNSZoneResourceGroup = '{{ .baseDnsZoneRG }}'
param regionalDNSSubdomain = '{{ .regionalDNSSubdomain }}'

// maestro
param maestroKeyVaultName = '{{ .maestro.keyVaultName }}'
param maestroEventGridNamespacesName = '{{ .maestro.eventGrid.name }}'
param maestroEventGridMaxClientSessionsPerAuthName = {{ .maestro.eventGrid.maxClientSessionsPerAuthName }}
param maestroEventGridMinimumTlsVersionAllowed = '{{ .maestro.eventGrid.minimumTlsVersionAllowed }}'
