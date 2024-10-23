using '../templates/region.bicep'

// dns
param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'global'
param regionalDNSSubdomain = 'westus3-b1ca8b'

// maestro
param maestroKeyVaultName = 'maestro-b1ca8'
param maestroEventGridNamespacesName = 'maestro-b1ca8'
param maestroEventGridMaxClientSessionsPerAuthName = 4
