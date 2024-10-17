using '../templates/region.bicep'

param persist = true

// dns
param baseDNSZoneName = 'hcp.osadev.cloud'
param baseDNSZoneResourceGroup = 'global'

// This parameter is always overriden in the Makefile
param currentUserId = ''
