using '../templates/region.bicep'

// dns
param baseDNSZoneName = '{{ .baseDnsZoneName }}'
param baseDNSZoneResourceGroup = '{{ .baseDnsZoneRG }}'
param regionalDNSSubdomain = '{{ .regionalDNSSubdomain }}'

// maestro
param maestroKeyVaultName = '{{ .maestroKeyVaultName }}'
param maestroEventGridNamespacesName = '{{ .maestroEventgridName }}'
param maestroEventGridMaxClientSessionsPerAuthName = {{ .maestroEventGridMaxClientSessionsPerAuthName }}
